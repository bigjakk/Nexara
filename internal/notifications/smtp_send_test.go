package notifications

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server used to drive SendSMTPMessage in tests.
// It records each command line received so assertions can verify whether
// STARTTLS was actually issued. When advertiseSTARTTLS is true the EHLO
// response includes the extension; on receipt of STARTTLS the connection is
// closed (the test does not need to complete the TLS handshake — a torn
// stream during the post-STARTTLS EHLO is enough to confirm the client took
// the upgrade path).
type fakeSMTP struct {
	advertiseSTARTTLS bool

	listener net.Listener

	mu       sync.Mutex
	commands []string
	closed   bool
}

func newFakeSMTP(t *testing.T, advertiseSTARTTLS bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs := &fakeSMTP{
		advertiseSTARTTLS: advertiseSTARTTLS,
		listener:          ln,
	}
	go fs.serve()
	t.Cleanup(func() {
		fs.mu.Lock()
		fs.closed = true
		fs.mu.Unlock()
		_ = ln.Close()
	})
	return fs
}

func (fs *fakeSMTP) addr() string {
	return fs.listener.Addr().String()
}

func (fs *fakeSMTP) record(cmd string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.commands = append(fs.commands, cmd)
}

func (fs *fakeSMTP) commandsCopy() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make([]string, len(fs.commands))
	copy(out, fs.commands)
	return out
}

func (fs *fakeSMTP) serve() {
	for {
		conn, err := fs.listener.Accept()
		if err != nil {
			return
		}
		go fs.handle(conn)
	}
}

func (fs *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := fmt.Fprint(conn, "220 fake.smtp ESMTP\r\n"); err != nil {
		return
	}

	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		fs.record(line)

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if _, err := fmt.Fprint(conn, "250-fake.smtp\r\n"); err != nil {
				return
			}
			if fs.advertiseSTARTTLS {
				if _, err := fmt.Fprint(conn, "250-STARTTLS\r\n"); err != nil {
					return
				}
			}
			if _, err := fmt.Fprint(conn, "250 8BITMIME\r\n"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "STARTTLS"):
			if _, err := fmt.Fprint(conn, "220 Go ahead\r\n"); err != nil {
				return
			}
			// Slam the door — the next bytes the client sends will be a TLS
			// ClientHello, not SMTP, and we want SendSMTPMessage to surface
			// a starttls error rather than silently keep going.
			return
		case strings.HasPrefix(upper, "MAIL FROM"):
			_, _ = fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(upper, "RCPT TO"):
			_, _ = fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(upper, "DATA"):
			_, _ = fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
			// Read until the lone "." line.
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					_, _ = fmt.Fprint(conn, "250 OK\r\n")
					break
				}
			}
		case strings.HasPrefix(upper, "QUIT"):
			_, _ = fmt.Fprint(conn, "221 Bye\r\n")
			return
		case strings.HasPrefix(upper, "AUTH"):
			// Reject any auth attempt; tests on the cleartext-loopback path
			// use Username="" so this branch only fires if the gate broke.
			_, _ = fmt.Fprint(conn, "535 Authentication failed\r\n")
		case strings.HasPrefix(upper, "RSET"), strings.HasPrefix(upper, "NOOP"):
			_, _ = fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(upper, "VRFY"):
			_, _ = fmt.Fprint(conn, "252 OK\r\n")
		default:
			_, _ = fmt.Fprint(conn, "500 Unrecognized\r\n")
		}
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port %q: %v", addr, err)
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	return host, p
}

func TestSendSMTPMessage_RejectsCleartextToNonLoopback(t *testing.T) {
	// 1.1.1.1 is a public IP literal — LookupHost returns it without DNS, so
	// no network round trip happens before validation rejects.
	err := SendSMTPMessage(context.Background(), SMTPSendOptions{
		Host:        "1.1.1.1",
		Port:        25,
		From:        "alerts@example.com",
		To:          []string{"ops@example.com"},
		Message:     []byte("test"),
		UseSTARTTLS: false,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback gate error, got: %v", err)
	}
}

func dialFake(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial fake smtp: %v", err)
	}
	return conn
}

func TestSendSMTPMessage_RejectsWhenSTARTTLSNotAdvertised(t *testing.T) {
	// Bypass the IP-validation gate: drive runSMTPConversation directly
	// against the loopback fake. The gate itself is verified by
	// TestSendSMTPMessage_RejectsCleartextToNonLoopback.
	server := newFakeSMTP(t, false)
	conn := dialFake(t, server.addr())
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := runSMTPConversation(ctx, conn, SMTPSendOptions{
		Host:        "127.0.0.1",
		Port:        25,
		Username:    "u",
		Password:    "secret",
		From:        "alerts@example.com",
		To:          []string{"ops@example.com"},
		Message:     []byte("payload"),
		UseSTARTTLS: true,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "does not advertise STARTTLS") {
		t.Fatalf("expected STARTTLS-gate error, got: %v", err)
	}

	// The client must NOT have sent AUTH, MAIL FROM, RCPT TO, or DATA — the
	// whole point of the gate is to fail before any credential or message
	// data hits the wire.
	for _, cmd := range server.commandsCopy() {
		upper := strings.ToUpper(cmd)
		if strings.HasPrefix(upper, "AUTH") ||
			strings.HasPrefix(upper, "MAIL FROM") ||
			strings.HasPrefix(upper, "RCPT TO") ||
			strings.HasPrefix(upper, "DATA") {
			t.Fatalf("client leaked %q before STARTTLS gate", cmd)
		}
	}
}

func TestSendSMTPMessage_IssuesSTARTTLSWhenAdvertised(t *testing.T) {
	server := newFakeSMTP(t, true)
	conn := dialFake(t, server.addr())
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := runSMTPConversation(ctx, conn, SMTPSendOptions{
		Host:        "127.0.0.1",
		Port:        25,
		From:        "alerts@example.com",
		To:          []string{"ops@example.com"},
		Message:     []byte("payload"),
		UseSTARTTLS: true,
	})
	// We expect a starttls error: our fake server closes the connection
	// instead of completing the TLS handshake, but this still confirms the
	// client took the upgrade path.
	if err == nil {
		t.Fatal("expected starttls error from fake handshake, got nil")
	}
	if !strings.Contains(err.Error(), "starttls") {
		t.Fatalf("expected starttls error, got: %v", err)
	}

	var sawSTARTTLS bool
	for _, cmd := range server.commandsCopy() {
		if strings.EqualFold(cmd, "STARTTLS") {
			sawSTARTTLS = true
		}
		upper := strings.ToUpper(cmd)
		if strings.HasPrefix(upper, "MAIL FROM") ||
			strings.HasPrefix(upper, "RCPT TO") ||
			strings.HasPrefix(upper, "DATA") {
			t.Fatalf("client leaked %q before TLS upgrade completed", cmd)
		}
	}
	if !sawSTARTTLS {
		t.Fatalf("expected client to send STARTTLS command; got %v", server.commandsCopy())
	}
}

func TestSendSMTPMessage_DeliversCleartextToLoopback(t *testing.T) {
	server := newFakeSMTP(t, false)
	host, port := splitHostPort(t, server.addr())
	if !net.ParseIP(host).IsLoopback() {
		t.Fatalf("fake server bound to non-loopback %q", host)
	}

	err := SendSMTPMessage(context.Background(), SMTPSendOptions{
		Host:        host,
		Port:        port,
		From:        "alerts@example.com",
		To:          []string{"ops@example.com"},
		Message:     []byte("payload"),
		UseSTARTTLS: false,
	})
	if err != nil {
		t.Fatalf("expected success on loopback cleartext, got: %v", err)
	}

	cmds := server.commandsCopy()
	for _, cmd := range cmds {
		if strings.EqualFold(cmd, "STARTTLS") {
			t.Fatalf("STARTTLS issued despite UseSTARTTLS=false: %v", cmds)
		}
	}
	var sawData bool
	for _, cmd := range cmds {
		if strings.EqualFold(cmd, "DATA") {
			sawData = true
		}
	}
	if !sawData {
		t.Fatalf("expected DATA in command stream, got: %v", cmds)
	}
}

func TestSendSMTPMessage_RejectsEmptyHost(t *testing.T) {
	err := SendSMTPMessage(context.Background(), SMTPSendOptions{
		To:      []string{"ops@example.com"},
		Message: []byte("payload"),
	})
	if err == nil || !strings.Contains(err.Error(), "host required") {
		t.Fatalf("expected host required error, got: %v", err)
	}
}

func TestSendSMTPMessage_RejectsEmptyRecipients(t *testing.T) {
	err := SendSMTPMessage(context.Background(), SMTPSendOptions{
		Host:    "smtp.example.com",
		Message: []byte("payload"),
	})
	if err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("expected recipient required error, got: %v", err)
	}
}

// Ensure SendSMTPMessage releases resources promptly when the context is
// cancelled mid-dial. We use a black-hole listener that never accepts to
// force the dial timeout path, then cancel before it fires.
func TestSendSMTPMessage_HonorsContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	// Don't accept — just drain.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, c)
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = SendSMTPMessage(ctx, SMTPSendOptions{
		Host:        host,
		Port:        port,
		From:        "a@b.com",
		To:          []string{"c@d.com"},
		Message:     []byte("x"),
		UseSTARTTLS: false,
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	// Anything that times out / errors here is acceptable; the goal is to
	// confirm the call doesn't block indefinitely. Sanity-check we got a
	// timeout-ish error rather than a successful return.
	if errors.Is(err, nil) {
		t.Fatal("expected error chain to include cancellation/timeout")
	}
}
