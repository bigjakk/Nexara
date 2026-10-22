package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testServer is a minimal in-process SSH server that:
//   - accepts password "test-pass" for username "test-user"
//   - replies to every "exec" request with "ok\n" on stdout, exit 0
//
// The host key is generated fresh per call. The returned address points at
// 127.0.0.1:<random-port>. Closing the listener stops the server.
func newTestServer(t *testing.T) (addr string, hostKey ssh.PublicKey, stop func()) {
	t.Helper()
	return newHoldingTestServer(t, nil, nil)
}

// newHoldingTestServer is newTestServer whose exec handler, when the channels
// are set, signals received as a command arrives and waits for release before
// it replies.
func newHoldingTestServer(t *testing.T, received chan<- struct{}, release <-chan struct{}) (addr string, hostKey ssh.PublicKey, stop func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("ssh signer: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == "test-user" && string(p) == "test-pass" {
				return nil, nil
			}
			return nil, errors.New("auth denied")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveOne(conn, cfg, received, release)
		}
	}()

	stop = func() {
		_ = ln.Close()
		<-done
	}
	return ln.Addr().String(), signer.PublicKey(), stop
}

func serveOne(rawConn net.Conn, cfg *ssh.ServerConfig, received chan<- struct{}, release <-chan struct{}) {
	defer func() { _ = rawConn.Close() }()
	sshConn, chans, reqs, err := ssh.NewServerConn(rawConn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sshConn.Close() }()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			return
		}
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			defer func() { _ = ch.Close() }()
			for req := range reqs {
				switch req.Type {
				case "exec":
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					if received != nil {
						received <- struct{}{}
						<-release
					}
					_, _ = io.WriteString(ch, "ok\n")
					_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					return
				default:
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
				}
			}
		}(ch, chReqs)
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	var port int
	if _, err := timeFmtAtoi(portStr, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}

func timeFmtAtoi(s string, out *int) (int, error) {
	// Tiny strconv-free atoi, keeps the test file's import surface minimal.
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("non-digit")
		}
		*out = (*out)*10 + int(ch-'0')
	}
	return *out, nil
}

func TestScanHostKey_returnsKey(t *testing.T) {
	addr, want, stop := newTestServer(t)
	defer stop()

	host, port := splitHostPort(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := ScanHostKey(ctx, host, port)
	if err != nil {
		t.Fatalf("ScanHostKey: %v", err)
	}
	if FingerprintSHA256(got) != FingerprintSHA256(want) {
		t.Fatalf("fingerprint mismatch: got %s, want %s",
			FingerprintSHA256(got), FingerprintSHA256(want))
	}
}

func TestExecute_rejectsMissingKnownHostKey(t *testing.T) {
	addr, _, stop := newTestServer(t)
	defer stop()

	host, port := splitHostPort(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Execute(ctx, Config{
		Host:     host,
		Port:     port,
		Username: "test-user",
		Password: "test-pass",
	}, "echo ok")
	if err == nil {
		t.Fatal("expected error when KnownHostKey is nil, got nil")
	}
	if !strings.Contains(err.Error(), "host key not pinned") {
		t.Fatalf("expected 'host key not pinned' error, got %q", err.Error())
	}
}

func TestExecute_succeedsWithMatchingKey(t *testing.T) {
	addr, hostKey, stop := newTestServer(t)
	defer stop()

	host, port := splitHostPort(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := Execute(ctx, Config{
		Host:         host,
		Port:         port,
		Username:     "test-user",
		Password:     "test-pass",
		KnownHostKey: hostKey,
	}, "echo ok")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "ok") {
		t.Fatalf("Stdout = %q, want to contain 'ok'", res.Stdout)
	}
}

func TestExecute_failsClosedOnHostKeyMismatch(t *testing.T) {
	addr, _, stop := newTestServer(t)
	defer stop()

	// Use a different freshly-generated key as the "expected" value.
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherPriv)
	wrongKey := otherSigner.PublicKey()

	host, port := splitHostPort(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Execute(ctx, Config{
		Host:         host,
		Port:         port,
		Username:     "test-user",
		Password:     "test-pass",
		KnownHostKey: wrongKey,
	}, "echo ok")
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *HostKeyMismatchError, got %T: %v", err, err)
	}
	if mismatch.PresentedFingerprint == "" || mismatch.ExpectedFingerprint == "" {
		t.Fatalf("mismatch error missing fingerprints: %+v", mismatch)
	}
	if mismatch.PresentedFingerprint == mismatch.ExpectedFingerprint {
		t.Fatalf("expected different fingerprints, got identical: %s",
			mismatch.PresentedFingerprint)
	}
}

func TestParseAuthorizedKey_roundTrip(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	pk := signer.PublicKey()

	encoded := MarshalAuthorizedKey(pk)
	if strings.HasSuffix(encoded, "\n") {
		t.Fatalf("MarshalAuthorizedKey should strip trailing newline, got %q", encoded)
	}

	decoded, err := ParseAuthorizedKey(encoded)
	if err != nil {
		t.Fatalf("ParseAuthorizedKey: %v", err)
	}
	if FingerprintSHA256(decoded) != FingerprintSHA256(pk) {
		t.Fatal("round-trip fingerprint mismatch")
	}
}

// stalledListener accepts TCP connections and never writes to them, like a
// wedged sshd or a forwarder whose backend is dead. Accepted conns arrive on
// the channel; any the test does not take are closed at cleanup.
func stalledListener(t *testing.T) (host string, port int, accepted <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ch := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			ch <- c
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		for {
			select {
			case c := <-ch:
				_ = c.Close()
			default:
				return
			}
		}
	})
	host, port = splitHostPort(t, ln.Addr().String())
	return host, port, ch
}

// A host that accepts TCP and never speaks SSH must release the caller when its
// context ends, and when the setup deadline passes: an HTTP handler's ctx is
// Background, so the deadline is its only bound. x/crypto applies
// ClientConfig.Timeout only inside ssh.Dial, which neither function uses.
func TestStalledHostReleasesTheCaller(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pinned, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	execCall := func(ctx context.Context, host string, port int, setup time.Duration) error {
		cfg := Config{Host: host, Port: port, Username: "test-user", Password: "test-pass", KnownHostKey: pinned}
		_, err := execute(ctx, cfg, "true", setup)
		return err
	}
	scanCall := func(ctx context.Context, host string, port int, setup time.Duration) error {
		_, err := scanHostKey(ctx, host, port, setup)
		return err
	}

	tests := []struct {
		name   string
		call   func(context.Context, string, int, time.Duration) error
		cancel bool // end the context; otherwise only the setup deadline can
	}{
		{"Execute, context ends", execCall, true},
		{"Execute, setup deadline passes", execCall, false},
		{"ScanHostKey, context ends", scanCall, true},
		{"ScanHostKey, setup deadline passes", scanCall, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			host, port, accepted := stalledListener(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			setup := time.Hour
			released := func(err error) bool { return errors.Is(err, context.Canceled) }
			if !tt.cancel {
				setup = 50 * time.Millisecond
				released = func(err error) bool {
					var ne net.Error
					return errors.As(err, &ne) && ne.Timeout()
				}
			}

			done := make(chan error, 1)
			go func() { done <- tt.call(ctx, host, port, setup) }()
			if tt.cancel {
				// Cancel once the caller is connected, so it is past the dial.
				select {
				case c := <-accepted:
					t.Cleanup(func() { _ = c.Close() })
				case err := <-done:
					t.Fatalf("returned before connecting: %v", err)
				}
				cancel()
			}
			select {
			case err := <-done:
				if !released(err) {
					t.Fatalf("err = %v, want the bound's error", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("still blocked 10s after its bound ended")
			}
		})
	}
}

// The setup bound is lifted before the command runs: a rolling update's apt
// run outlives it by minutes. The server holds its reply until a stalled scan
// with the same bound has timed out, so the command provably outlasts it.
func TestExecute_commandMayOutliveTheSetupBound(t *testing.T) {
	t.Parallel()
	const bound = 2 * time.Second
	received, release := make(chan struct{}, 1), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	addr, hostKey, stop := newHoldingTestServer(t, received, release)
	defer stop()
	host, port := splitHostPort(t, addr)

	done := make(chan error, 1)
	go func() {
		cfg := Config{Host: host, Port: port, Username: "test-user", Password: "test-pass", KnownHostKey: hostKey}
		_, err := execute(context.Background(), cfg, "true", bound)
		done <- err
	}()
	select {
	case <-received:
	case err := <-done:
		t.Fatalf("returned before the command ran: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the command never reached the server")
	}
	sHost, sPort, _ := stalledListener(t)
	scanned := make(chan error, 1)
	go func() {
		_, err := scanHostKey(context.Background(), sHost, sPort, bound)
		scanned <- err
	}()
	select {
	case err := <-scanned:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("the stalled scan ended with %v, not on its bound", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stalled scan outlived its bound by 8s")
	}
	releaseOnce()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a command that outlived the setup bound failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no result 10s after the reply was released")
	}
}
