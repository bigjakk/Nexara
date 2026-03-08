// Package syslog provides RFC 5424 syslog forwarding for audit events.
package syslog

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Config holds syslog forwarding configuration.
type Config struct {
	Enabled       bool   `json:"enabled"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Protocol      string `json:"protocol"`        // "udp", "tcp", "tls"
	Facility      int    `json:"facility"`         // syslog facility (0-23), default 16 (local0)
	TLSSkipVerify bool   `json:"tls_skip_verify"`  // skip TLS certificate verification
}

// Message represents an audit event to be forwarded as a syslog message.
type Message struct {
	Timestamp    time.Time
	UserID       string
	ClusterID    string
	ResourceType string
	ResourceID   string
	Action       string
	Details      string // raw JSON
}

// Forwarder sends audit events to a remote syslog server.
type Forwarder struct {
	mu     sync.RWMutex
	config Config
	conn   net.Conn
	logger *slog.Logger
}

// NewForwarder creates a new syslog forwarder.
func NewForwarder(logger *slog.Logger) *Forwarder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Forwarder{logger: logger}
}

// Configure updates the forwarder configuration and reconnects if needed.
func (f *Forwarder) Configure(cfg Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Close existing connection.
	if f.conn != nil {
		_ = f.conn.Close()
		f.conn = nil
	}

	f.config = cfg

	if !cfg.Enabled {
		return nil
	}

	return f.connectLocked()
}

// Config returns the current configuration.
func (f *Forwarder) Config() Config {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.config
}

// Forward sends an audit message to the syslog server.
// It is safe for concurrent use and silently drops messages if not enabled.
func (f *Forwarder) Forward(msg Message) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.config.Enabled {
		return
	}

	formatted := f.formatRFC5424(msg)

	if f.conn == nil {
		if err := f.connectLocked(); err != nil {
			f.logger.Warn("syslog: failed to connect", "error", err)
			return
		}
	}

	if err := f.sendLocked(formatted); err != nil {
		// Reconnect once and retry.
		f.logger.Debug("syslog: send failed, reconnecting", "error", err)
		_ = f.conn.Close()
		f.conn = nil
		if err := f.connectLocked(); err != nil {
			f.logger.Warn("syslog: reconnect failed", "error", err)
			return
		}
		if err := f.sendLocked(formatted); err != nil {
			f.logger.Warn("syslog: retry send failed", "error", err)
		}
	}
}

// Test sends a test message and returns any error.
func (f *Forwarder) Test(cfg Config) error {
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	conn, err := f.dial(cfg.Protocol, addr, cfg.TLSSkipVerify)
	if err != nil {
		return fmt.Errorf("connect to %s://%s: %w", cfg.Protocol, addr, err)
	}
	defer func() { _ = conn.Close() }()

	facility := cfg.Facility
	if facility == 0 {
		facility = 16
	}
	pri := facility*8 + 6 // informational

	msg := fmt.Sprintf("<%d>1 %s proxdash audit - - - ProxDash syslog forwarding test message\n",
		pri,
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	)

	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write([]byte(msg))
	if err != nil {
		return fmt.Errorf("write test message: %w", err)
	}

	return nil
}

// Close closes the underlying connection.
func (f *Forwarder) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		_ = f.conn.Close()
		f.conn = nil
	}
}

func (f *Forwarder) connectLocked() error {
	cfg := f.config
	if cfg.Host == "" || cfg.Port == 0 {
		return fmt.Errorf("syslog host and port are required")
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	conn, err := f.dial(cfg.Protocol, addr, cfg.TLSSkipVerify)
	if err != nil {
		return err
	}

	f.conn = conn
	return nil
}

func (f *Forwarder) dial(protocol, addr string, tlsSkipVerify bool) (net.Conn, error) {
	switch strings.ToLower(protocol) {
	case "tls":
		return tls.DialWithDialer(
			&net.Dialer{Timeout: 5 * time.Second},
			"tcp",
			addr,
			&tls.Config{InsecureSkipVerify: tlsSkipVerify}, //nolint:gosec // user-configured option
		)
	case "tcp":
		return net.DialTimeout("tcp", addr, 5*time.Second)
	case "udp", "":
		return net.DialTimeout("udp", addr, 5*time.Second)
	default:
		return nil, fmt.Errorf("unsupported syslog protocol: %s", protocol)
	}
}

func (f *Forwarder) sendLocked(data []byte) error {
	_ = f.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err := f.conn.Write(data)
	return err
}

func (f *Forwarder) formatRFC5424(msg Message) []byte {
	facility := f.config.Facility
	if facility == 0 {
		facility = 16 // local0
	}

	severity := actionSeverity(msg.Action)
	pri := facility*8 + severity

	ts := msg.Timestamp.UTC().Format("2006-01-02T15:04:05Z")

	clusterID := msg.ClusterID
	if clusterID == "" {
		clusterID = "system"
	}

	body := fmt.Sprintf("user=%s cluster=%s resource_type=%s resource_id=%s action=%s",
		msg.UserID, clusterID, msg.ResourceType, msg.ResourceID, msg.Action)

	if msg.Details != "" && msg.Details != "{}" {
		body += fmt.Sprintf(" details=%s", msg.Details)
	}

	line := fmt.Sprintf("<%d>1 %s proxdash audit - - - %s\n", pri, ts, body)
	return []byte(line)
}

// actionSeverity maps action names to syslog severity levels.
func actionSeverity(action string) int {
	a := strings.ToLower(action)
	switch {
	case strings.Contains(a, "error") || strings.Contains(a, "failed") || strings.Contains(a, "fail"):
		return 3 // error
	case strings.Contains(a, "delete") || strings.Contains(a, "destroy") ||
		strings.Contains(a, "disable") || strings.Contains(a, "revoke") ||
		strings.Contains(a, "reset"):
		return 4 // warning
	default:
		return 6 // informational
	}
}
