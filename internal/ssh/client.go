// Package ssh provides SSH command execution for Proxmox node management.
//
// All connections require a pinned host key (TOFU model). Callers must scan
// and persist the host key before invoking Execute or TestConnection;
// connections fail closed when no key is supplied.
package ssh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/bigjakk/nexara/internal/netguard"
)

// ExecResult contains the output and exit status of a remote command.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Config holds SSH connection parameters.
//
// Password and PrivateKey are live node credentials, held in plaintext for the
// lifetime of one connection. String, GoString, LogValue and MarshalJSON keep
// them out of every rendering that dispatches on the type, because this struct
// is passed BY VALUE into code that logs around it. Of the three construction
// sites, internal/rolling/orchestrator.go builds one a few lines from a log
// call naming the node; internal/rolling/nodessh.go and
// internal/api/handlers/rolling_update.go hand theirs straight to Execute or
// TestConnection. Nothing renders a Config today, so this is latent rather
// than a live leak; the point is that the next `%+v` or
// `slog.Any("cfg", cfg)` someone writes while debugging a failed node upgrade
// is already closed.
//
// Between them the four methods cover: %v, %s, %q, %x, %X, %+v, fmt.Sprint and
// error wrapping via String; %#v via GoString (fmt dispatches GoStringer there,
// NOT Stringer, which is the one that is easy to forget); structured logs via
// LogValue; and encoding/json via MarshalJSON — including a Config reached as
// an EXPORTED field of a larger struct.
//
// What is NOT covered, listed so the set above is not mistaken for
// "everything". None is reachable today; they are recorded because the next
// person to create one of these shapes should know it is not covered:
//
//   - A verb fmt cannot dispatch these for — %d, %t, %p, %c, %f — falls back
//     to printing the fields and shows both secrets.
//   - A Config in an UNEXPORTED field. fmt cannot call a method through one,
//     so %+v and %#v of the outer struct print the raw fields.
//   - An ANONYMOUS embed. `struct{ Config; Extra string }` promotes these
//     methods to the outer type, so json.Marshal emits only the redacted
//     object and silently drops Extra, and %v renders only the inner. Safe for
//     the secrets, wrong for everything else — embed it as a NAMED field.
//   - Reflection encoders that ignore MarshalJSON: encoding/xml and
//     encoding/gob both emit the secrets verbatim.
//
// Host, Port and Username stay visible. None is a secret, and a redacted
// rendering that identifies nothing is not worth emitting. KnownHostKey is
// dropped rather than redacted: it is a public key, not a secret, but it adds
// nothing to a log line that the host does not already say.
//
// All four have VALUE receivers on purpose. fmt and encoding/json skip a
// pointer-receiver method on a value they cannot address, and every call site
// passes a Config by value, so a pointer receiver here would compile, lint
// clean and redact nothing.
type Config struct {
	Host       string
	Port       int
	Username   string
	Password   string
	PrivateKey string
	// KnownHostKey is the pinned remote public key. Required — the connection
	// fails closed when nil. Use ScanHostKey to retrieve it before pinning.
	KnownHostKey ssh.PublicKey
}

// String is the redacted rendering reached by %v, %s, fmt.Sprint and error
// wrapping.
func (c Config) String() string {
	return "ssh.Config{host:" + c.Host + " port:" + strconv.Itoa(c.Port) +
		" username:" + c.Username + " password:REDACTED privatekey:REDACTED}"
}

// GoString closes the route String cannot: %#v dispatches GoStringer, and
// without this it prints the struct literal with both secrets in it — for the
// value, for a pointer to it, and for anything holding one as a field.
func (c Config) GoString() string {
	return `ssh.Config{Host:"` + c.Host + `", Port:` + strconv.Itoa(c.Port) +
		`, Username:"` + c.Username + `", Password:"REDACTED", PrivateKey:"REDACTED"}`
}

// LogValue keeps the credentials out of structured logs while leaving enough to
// say which connection a line is about.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", c.Host),
		slog.Int("port", c.Port),
		slog.String("username", c.Username),
	)
}

// MarshalJSON closes the encoding/json route. Only the marshal direction is
// overridden — UnmarshalJSON is a separate interface, so decoding is
// unaffected. Nothing decodes a Config today; anything that starts to must
// carry Password, PrivateKey and KnownHostKey itself rather than expect them
// back out. Dropping the pinned host key here is deliberate and fails closed:
// a Config round-tripped through JSON has no key, and Execute refuses to
// connect without one.
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
	}{c.Host, c.Port, c.Username})
}

// HostKeyMismatchError indicates the remote presented a key that does not
// match the pinned key. The handshake aborts before any auth is attempted.
type HostKeyMismatchError struct {
	Host                 string
	ExpectedFingerprint  string
	PresentedFingerprint string
	PresentedPublicKey   string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("SSH host key mismatch for %s: expected %s, presented %s",
		e.Host, e.ExpectedFingerprint, e.PresentedFingerprint)
}

// MarshalAuthorizedKey serializes a public key for storage in the
// ssh_known_hosts table. The trailing newline is stripped.
func MarshalAuthorizedKey(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

// ParseAuthorizedKey parses a stored public key.
func ParseAuthorizedKey(authorized string) (ssh.PublicKey, error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorized))
	if err != nil {
		return nil, fmt.Errorf("parse SSH public key: %w", err)
	}
	return pk, nil
}

// FingerprintSHA256 returns the SHA256 fingerprint of the key in OpenSSH format.
func FingerprintSHA256(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// errCaptureDone is returned by the host-key callback in ScanHostKey to
// abort the handshake immediately after the key is captured. It is never
// surfaced to callers.
var errCaptureDone = errors.New("nexara/ssh: host key captured")

// ScanHostKey opens a TCP connection long enough to capture the remote host
// key, then closes it without attempting authentication. The caller pins
// the returned key after confirming its fingerprint with the user.
func ScanHostKey(ctx context.Context, host string, port int) (ssh.PublicKey, error) {
	if host == "" {
		return nil, fmt.Errorf("scan SSH host key: host is empty")
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("scan SSH host key: invalid port %d", port)
	}

	var captured ssh.PublicKey
	cb := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		// MarshalAuthorizedKey + ParseAuthorizedKey rounds the key through
		// the storage form so the caller stores the same bytes we'll later
		// compare against.
		captured = key
		return errCaptureDone
	}

	cfg := &ssh.ClientConfig{
		User:            "nexara-scan",
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: cb,
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer := net.Dialer{Control: netguard.DialControlSSRFGuard}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	// We expect NewClientConn to return errCaptureDone (propagated from our
	// callback). Any other error — or a successful handshake (which would
	// be surprising given the empty Auth slice) — means the handshake did
	// not exit via our captured-key path, so we must NOT trust `captured`.
	_, _, _, handshakeErr := ssh.NewClientConn(conn, addr, cfg)
	if !errors.Is(handshakeErr, errCaptureDone) {
		if handshakeErr != nil {
			return nil, fmt.Errorf("scan SSH host key: handshake failed: %w", handshakeErr)
		}
		return nil, fmt.Errorf("scan SSH host key: handshake completed unexpectedly")
	}
	if captured == nil {
		return nil, fmt.Errorf("scan SSH host key: handshake did not present a host key")
	}
	return captured, nil
}

// Execute runs a command on a remote host via SSH.
// The context controls the overall timeout including connection and execution.
func Execute(ctx context.Context, cfg Config, command string) (*ExecResult, error) {
	if cfg.KnownHostKey == nil {
		return nil, fmt.Errorf("SSH host key not pinned for %s — pin the key before connecting", cfg.Host)
	}

	authMethods, err := buildAuth(cfg)
	if err != nil {
		return nil, fmt.Errorf("build SSH auth: %w", err)
	}

	expectedFP := ssh.FingerprintSHA256(cfg.KnownHostKey)
	expectedBytes := cfg.KnownHostKey.Marshal()

	sshCfg := &ssh.ClientConfig{
		User: cfg.Username,
		Auth: authMethods,
		HostKeyCallback: func(hostname string, _ net.Addr, presented ssh.PublicKey) error {
			if bytes.Equal(presented.Marshal(), expectedBytes) {
				return nil
			}
			return &HostKeyMismatchError{
				Host:                 hostname,
				ExpectedFingerprint:  expectedFP,
				PresentedFingerprint: ssh.FingerprintSHA256(presented),
				PresentedPublicKey:   MarshalAuthorizedKey(presented),
			}
		},
		Timeout: 30 * time.Second,
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	// Dial with context-aware timeout. Control hook hard-blocks cloud
	// metadata, multicast, broadcast, Class E, and unspecified IPs so a
	// rebinding-style DNS poisoning cannot redirect us mid-connect.
	dialer := net.Dialer{Control: netguard.DialControlSSRFGuard}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		_ = conn.Close()
		// Preserve the typed mismatch error so callers can prompt the user.
		var mismatch *HostKeyMismatchError
		if errors.As(err, &mismatch) {
			return nil, mismatch
		}
		return nil, fmt.Errorf("SSH handshake with %s: %w", addr, err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Run the command with context cancellation.
	done := make(chan error, 1)
	go func() {
		done <- session.Run(command)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		return nil, fmt.Errorf("SSH command timed out: %w", ctx.Err())
	case err := <-done:
		result := &ExecResult{
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			ExitCode: 0,
		}
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitStatus()
			} else {
				return nil, fmt.Errorf("SSH command failed: %w", err)
			}
		}
		return result, nil
	}
}

// TestConnection verifies SSH connectivity and auth to a host.
// The caller must populate cfg.KnownHostKey with the pinned key.
func TestConnection(ctx context.Context, cfg Config) error {
	result, err := Execute(ctx, cfg, "echo ok")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("test command exited with code %d: %s", result.ExitCode, result.Stderr)
	}
	return nil
}

func buildAuth(cfg Config) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if cfg.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth methods configured")
	}

	return methods, nil
}
