package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
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
			go serveOne(conn, cfg)
		}
	}()

	stop = func() {
		_ = ln.Close()
		<-done
	}
	return ln.Addr().String(), signer.PublicKey(), stop
}

func serveOne(rawConn net.Conn, cfg *ssh.ServerConfig) {
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
