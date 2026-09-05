package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	gorillaws "github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
)

type testEnv struct {
	redisClient *redis.Client
	jwtSvc      *auth.JWTService
	port        int
}

func setupIntegration(t *testing.T) *testEnv {
	t.Helper()

	mr := miniredis.RunT(t)

	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	logger := testLogger()

	hub := NewHub(logger, 0)
	hub.Run()

	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	// Test-mode permission checker grants every action — the integration
	// suite exercises subscribe/publish wiring, not the gate (covered
	// in subscribe_auth_test.go). Production wires RBACEngine instead.
	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second, ServerConfig{
		TestPermissionChecker: func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
			return true, nil
		},
	})

	// Start Redis subscriber.
	ctx, cancel := context.WithCancel(context.Background())
	sub := NewRedisSubscriber(redisClient, hub, logger)
	go sub.Run(ctx)

	// Registered before the server starts, not after: startTestServer fatals
	// when the server never becomes reachable, and the hub goroutine and the
	// Redis subscriber still need tearing down on that path. Order inside
	// matters — the server stops accepting before the hub it publishes into
	// goes away, and Shutdown on an app that never listened is a no-op.
	t.Cleanup(func() {
		cancel()
		server.Shutdown()
		hub.Stop()
		redisClient.Close()
	})

	port := startTestServer(t, server)

	return &testEnv{
		redisClient: redisClient,
		jwtSvc:      jwtSvc,
		port:        port,
	}
}

// mintHubToken issues a token for a fresh user. Per remediation 2.7, the /ws
// upgrade requires a hub-scoped token; the long-lived access token is rejected,
// so tests that open the hub path mint one (≤60s TTL) the way the SPA does.
func mintHubToken(t *testing.T, jwtSvc *auth.JWTService) string {
	t.Helper()
	token, _, err := jwtSvc.GenerateWSHubToken(uuid.New(), "test@example.com", "admin", 60*time.Second)
	if err != nil {
		t.Fatalf("generate hub token: %v", err)
	}
	return token
}

func (e *testEnv) dialWS(t *testing.T, token string) *gorillaws.Conn {
	t.Helper()
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", e.port, token)
	conn, _, err := gorillaws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

// dialWSSubprotocol exercises the Sec-WebSocket-Protocol auth path —
// `nexara.token, nexara.token.<jwt>`. The server must echo back
// `nexara.token` for the connection to open.
func (e *testEnv) dialWSSubprotocol(t *testing.T, token string) *gorillaws.Conn {
	t.Helper()
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", e.port)
	dialer := *gorillaws.DefaultDialer
	dialer.Subprotocols = []string{"nexara.token", "nexara.token." + token}
	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "nexara.token" {
		t.Errorf("expected echo Sec-WebSocket-Protocol=nexara.token, got %q", got)
	}
	return conn
}

func readMsg(t *testing.T, conn *gorillaws.Conn, timeout time.Duration) OutgoingMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	var msg OutgoingMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return msg
}

func writeMsg(t *testing.T, conn *gorillaws.Conn, msg IncomingMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(gorillaws.TextMessage, data); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

func TestIntegrationHealthz(t *testing.T) {
	env := setupIntegration(t)

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", env.port)
	resp, err := http.Get(url) //nolint:gosec // test
	if err != nil {
		t.Fatalf("healthz request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegrationWSConnectAndWelcome(t *testing.T) {
	env := setupIntegration(t)

	token := mintHubToken(t, env.jwtSvc)
	conn := env.dialWS(t, token)
	defer conn.Close()

	msg := readMsg(t, conn, 3*time.Second)
	if msg.Type != MsgTypeWelcome {
		t.Errorf("expected welcome, got %s", msg.Type)
	}
}

// TestIntegrationWSConnectViaSubprotocol verifies the preferred auth path:
// the JWT rides in `Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>`
// instead of the URL. Server echoes `nexara.token` back, browser opens the
// connection cleanly.
func TestIntegrationWSConnectViaSubprotocol(t *testing.T) {
	env := setupIntegration(t)

	token := mintHubToken(t, env.jwtSvc)
	conn := env.dialWSSubprotocol(t, token)
	defer conn.Close()

	msg := readMsg(t, conn, 3*time.Second)
	if msg.Type != MsgTypeWelcome {
		t.Errorf("expected welcome, got %s", msg.Type)
	}
}

// TestIntegrationWSAccessTokenRejected confirms that a plain access token
// (the kind the API uses for Authorization: Bearer) is REJECTED on the
// /ws upgrade. Per remediation 2.7, only hub-scoped tokens are accepted.
func TestIntegrationWSAccessTokenRejected(t *testing.T) {
	env := setupIntegration(t)

	userID := uuid.New()
	accessToken, _, err := env.jwtSvc.GenerateAccessToken(userID, "test@example.com", "admin")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", env.port, accessToken)
	_, resp, err := gorillaws.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected error: access token must not be accepted on /ws")
	}
	if resp == nil || resp.StatusCode != 403 {
		t.Errorf("expected 403, got %v", resp)
	}
}

func TestIntegrationWSNoToken(t *testing.T) {
	env := setupIntegration(t)

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", env.port)
	_, resp, err := gorillaws.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if resp != nil && resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestIntegrationSubscribeAndReceive(t *testing.T) {
	env := setupIntegration(t)

	token := mintHubToken(t, env.jwtSvc)
	conn := env.dialWS(t, token)
	defer conn.Close()

	// Read welcome.
	welcome := readMsg(t, conn, 3*time.Second)
	if welcome.Type != MsgTypeWelcome {
		t.Fatalf("expected welcome, got %s", welcome.Type)
	}

	clusterUUID := "550e8400-e29b-41d4-a716-446655440000"
	room := fmt.Sprintf("cluster:%s:metrics", clusterUUID)

	// Subscribe.
	writeMsg(t, conn, IncomingMessage{
		Type:     MsgTypeSubscribe,
		Channels: []string{room},
	})

	// Read subscribed confirmation.
	sub := readMsg(t, conn, 3*time.Second)
	if sub.Type != MsgTypeSubscribed || sub.Channel != room {
		t.Fatalf("expected subscribed for %s, got %+v", room, sub)
	}

	// Publish metrics via Redis (like the collector does).
	redisChannel := fmt.Sprintf("nexara:metrics:%s", clusterUUID)
	payload := `{"cpu":0.75,"mem_used":1024}`
	if err := env.redisClient.Publish(context.Background(), redisChannel, payload).Err(); err != nil {
		t.Fatalf("redis publish: %v", err)
	}

	// Read the data message.
	data := readMsg(t, conn, 3*time.Second)
	if data.Type != MsgTypeData {
		t.Fatalf("expected data, got %s", data.Type)
	}
	if data.Channel != room {
		t.Errorf("expected channel %s, got %s", room, data.Channel)
	}
}

func TestIntegrationPingPong(t *testing.T) {
	env := setupIntegration(t)

	token := mintHubToken(t, env.jwtSvc)
	conn := env.dialWS(t, token)
	defer conn.Close()

	// Read welcome.
	readMsg(t, conn, 3*time.Second)

	// Send application-level ping.
	writeMsg(t, conn, IncomingMessage{Type: MsgTypePing})

	// Read pong.
	pong := readMsg(t, conn, 3*time.Second)
	if pong.Type != MsgTypePong {
		t.Errorf("expected pong, got %s", pong.Type)
	}
}
