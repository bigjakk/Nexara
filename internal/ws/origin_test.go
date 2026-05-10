package ws

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

func TestParseAllowedOrigins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"wildcard alone", "*", nil},
		{"wildcard with whitespace", "  *  ", nil},
		{"wildcard mixed in list shortcircuits", "https://a.example,*,https://b.example", nil},
		{"single origin", "https://nexara.example.com", []string{"https://nexara.example.com"}},
		{
			"two origins",
			"https://a.example,https://b.example",
			[]string{"https://a.example", "https://b.example"},
		},
		{
			"trims whitespace",
			"  https://a.example , https://b.example  ",
			[]string{"https://a.example", "https://b.example"},
		},
		{
			"drops empty entries",
			"https://a.example,,,https://b.example,",
			[]string{"https://a.example", "https://b.example"},
		},
		{"preserves port in origin", "https://nexara.example:8443", []string{"https://nexara.example:8443"}},
		{"only commas + whitespace returns nil", " , , ", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAllowedOrigins(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseAllowedOrigins(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestWSConfigWithSubprotocol_OriginsPropagation(t *testing.T) {
	t.Parallel()

	// nil origins → upgrader gets no Origins set, gofiber's default kicks
	// in (allow-all). This is the legacy permissive behaviour we want
	// preserved when the operator hasn't configured an allow-list.
	zero := wsConfigWithSubprotocol(nil)
	if len(zero.Origins) != 0 {
		t.Errorf("nil origins must produce empty Origins slice, got %v", zero.Origins)
	}
	if len(zero.Subprotocols) != 1 || zero.Subprotocols[0] != subprotocolNegotiationName {
		t.Errorf("expected static subprotocol, got %v", zero.Subprotocols)
	}

	// Non-nil origins → upgrader gets a copy of the slice. Caller mutation
	// after construction must not affect the upgrader's view.
	src := []string{"https://a.example", "https://b.example"}
	cfg := wsConfigWithSubprotocol(src)
	if !reflect.DeepEqual(cfg.Origins, src) {
		t.Errorf("Origins not copied through: got %v, want %v", cfg.Origins, src)
	}
	src[0] = "https://attacker.example"
	if cfg.Origins[0] != "https://a.example" {
		t.Errorf("upgrader Origins shared backing array with caller — got %q after caller mutation", cfg.Origins[0])
	}
}

// TestIntegrationOriginRejection mounts a server with an explicit allow-list
// and confirms that a WS upgrade carrying an Origin not in the list is
// rejected before the auth middleware runs the JWT.
//
// Per the gofiber/contrib upgrader, CheckOrigin runs inside Upgrade() and
// — when it returns false — replies with HTTP 403 (the upgrader writes
// the response itself). The wrapper then surfaces an error which Fiber's
// default error handler may overwrite with a different status, but the
// dial fails either way.
func TestIntegrationOriginRejection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(logger, 0)
	hub.Run()
	defer hub.Stop()

	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second, ServerConfig{
		AllowedOrigins: []string{"https://allowed.example"},
	})

	port := startTestServer(t, server)
	defer server.Shutdown()

	mintHubToken := func(t *testing.T) string {
		t.Helper()
		tok, _, err := jwtSvc.GenerateWSHubToken(uuid.New(), "test@example.com", "admin", 60*time.Second)
		if err != nil {
			t.Fatalf("generate hub token: %v", err)
		}
		return tok
	}

	dial := func(t *testing.T, origin string) (*gorillaws.Conn, *http.Response, error) {
		t.Helper()
		token := mintHubToken(t)
		dialer := *gorillaws.DefaultDialer
		dialer.HandshakeTimeout = 3 * time.Second
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		u := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", port), Path: "/ws"}
		q := u.Query()
		q.Set("token", token)
		u.RawQuery = q.Encode()
		return dialer.Dial(u.String(), hdr)
	}

	t.Run("disallowed origin rejected", func(t *testing.T) {
		conn, resp, err := dial(t, "https://attacker.example")
		if err == nil {
			conn.Close()
			t.Fatal("expected dial failure for disallowed origin")
		}
		if resp == nil {
			t.Fatalf("expected HTTP response, got nil; err=%v", err)
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSwitchingProtocols {
			t.Errorf("expected non-success status, got %d", resp.StatusCode)
		}
	})

	t.Run("missing origin rejected", func(t *testing.T) {
		// fasthttp/websocket's default CheckOrigin treats missing
		// Origin as same-origin and allows it, but our wrapper supplies
		// an explicit Origins list, so the missing-Origin case fails the
		// exact-match check.
		conn, resp, err := dial(t, "")
		if err == nil {
			conn.Close()
			t.Fatal("expected dial failure for missing Origin header")
		}
		if resp == nil {
			t.Fatalf("expected HTTP response for missing Origin, got nil; err=%v", err)
		}
	})

	t.Run("allowed origin accepted", func(t *testing.T) {
		conn, _, err := dial(t, "https://allowed.example")
		if err != nil {
			t.Fatalf("dial with allowed origin failed: %v", err)
		}
		defer conn.Close()
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("read welcome: %v", err)
		}
	})
}

// TestIntegrationOriginPermissiveDefault confirms that with an empty
// AllowedOrigins (the dev-friendly default), upgrades from any origin
// continue to succeed — non-breaking for existing self-hosted installs.
func TestIntegrationOriginPermissiveDefault(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(logger, 0)
	hub.Run()
	defer hub.Stop()

	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second)
	port := startTestServer(t, server)
	defer server.Shutdown()

	tok, _, err := jwtSvc.GenerateWSHubToken(uuid.New(), "test@example.com", "admin", 60*time.Second)
	if err != nil {
		t.Fatalf("generate hub token: %v", err)
	}

	dialer := *gorillaws.DefaultDialer
	dialer.HandshakeTimeout = 3 * time.Second
	hdr := http.Header{"Origin": []string{"https://anything.example"}}
	u := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", port, tok)
	conn, _, err := dialer.Dial(u, hdr)
	if err != nil {
		t.Fatalf("dial with permissive default failed: %v", err)
	}
	defer conn.Close()
}

// TestConsoleSetReadLimitConstant locks the configured read-limit down at
// the documented 64 KiB. A future tuning should be deliberate (with the
// doc-comment updated) rather than a silent 10× bump.
func TestConsoleSetReadLimitConstant(t *testing.T) {
	t.Parallel()
	if MaxBrowserConsoleMessageBytes != 64*1024 {
		t.Errorf("MaxBrowserConsoleMessageBytes = %d, want %d", MaxBrowserConsoleMessageBytes, 64*1024)
	}
}

// startTestServer starts a Server on an ephemeral port and waits for it to
// become reachable. Returns the bound port. Used by the origin tests above.
func startTestServer(t *testing.T, s *Server) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	go func() {
		_ = s.Listen(port)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			c.Close()
			return port
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never became reachable on port %d", port)
	return 0
}
