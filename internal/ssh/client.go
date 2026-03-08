// Package ssh provides SSH command execution for Proxmox node management.
package ssh

import (
	"bytes"
	"context"
	"fmt"
	"net"
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
}

// Execute runs a command on a remote host via SSH.
// The context controls the overall timeout including connection and execution.
func Execute(ctx context.Context, cfg Config, command string) (*ExecResult, error) {
	authMethods, err := buildAuth(cfg)
	if err != nil {
		return nil, fmt.Errorf("build SSH auth: %w", err)
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // Proxmox nodes are trusted internal infra
		Timeout:         30 * time.Second,
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
			if exitErr, ok := err.(*ssh.ExitError); ok {
				result.ExitCode = exitErr.ExitStatus()
			} else {
				return nil, fmt.Errorf("SSH command failed: %w", err)
			}
		}
		return result, nil
	}
}

// TestConnection verifies SSH connectivity and auth to a host.
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
