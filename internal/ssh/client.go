// Package ssh provides SSH command execution for Proxmox node management.
//
// All connections require a pinned host key (TOFU model). Callers must scan
// and persist the host key before invoking Execute or TestConnection;
// connections fail closed when no key is supplied.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ExecResult contains the output and exit status of a remote command.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Config holds SSH connection parameters.
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

// HostKeyMismatchError indicates the remote presented a key that does not
// match the pinned key. The handshake aborts before any auth is attempted.
type HostKeyMismatchError struct {
	Host                string
	ExpectedFingerprint string
	PresentedFingerprint string
	PresentedPublicKey  string
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
	var dialer net.Dialer
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

	// Dial with context-aware timeout.
	var dialer net.Dialer
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
