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

	msg := fmt.Sprintf("<%d>1 %s nexara audit - - - Nexara syslog forwarding test message\n",
		pri,
		time.Now().UTC().Format(time.RFC3339Nano),
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

// FormatAuditBody renders the flat key=value MSG body that both the live
// forwarder and the audit-log syslog export emit. It is shared so the two
// cannot drift — they already had, the export quoting user and cluster while
// leaving resource_type, resource_id, action and details raw.
//
// Quoting every value with %q is load-bearing, not cosmetic. The body sits in
// RFC 5424's free-form MSG, which collectors parse as key=value. An unquoted
// value carrying a space forges fields: a resource id of
//
//	x action=login user=root
//
// arrives at the SIEM as a different action by a different user. An unquoted
// newline is worse — it terminates the record and starts one the caller wrote
// in full, so a single audit row can inject wholly fabricated events.
//
// %q buys two different guarantees, worth separating:
//
//   - Control characters — \n, \r, tabs — become escapes, so a value can never
//     end the record or start a new one. This holds for every collector,
//     including one that just splits the stream on newlines.
//   - Spaces stay inside the quotes, so a value cannot open a new field. This
//     one is conditional on the collector, and the condition is narrower than
//     "quote-aware". %q emits Go literal escaping, so the guarantee is exact
//     only for a reader that also honors backslash escapes — logfmt, which
//     decodes with strconv.Unquote. An escape-blind extractor of the
//     `key="([^"]*)"` shape (Splunk's default KV_MODE, Logstash's kv filter)
//     ends the value at the first literal quote even when it is escaped, so
//     a value of `prod" action="login` renders as "prod\" action=\"login" and
//     can still surface a fabricated action field there.
//
// So field forging is reduced, not eliminated. It is strictly better than the
// unquoted form it replaces — that forged fields against every parser, strict
// ones included, and left no artifacts — but a whitespace-splitting or
// escape-blind collector can still be partly confused. Closing it outright
// means moving these into RFC 5424 STRUCTURED-DATA, whose escaping is
// spec-defined and uniform across conforming parsers; that is a wire-format
// change and belongs in its own release, not here.
//
// This matters because the inputs are caller-influenced: audit_log.details and
// resource_id are populated from request data by many handlers.
func FormatAuditBody(user, cluster, resourceType, resourceID, action, details string) string {
	body := fmt.Sprintf("user=%q cluster=%q resource_type=%q resource_id=%q action=%q",
		user, cluster, resourceType, resourceID, action)
	if details != "" && details != "{}" {
		body += fmt.Sprintf(" details=%q", details)
	}
	return body
}

func (f *Forwarder) formatRFC5424(msg Message) []byte {
	facility := f.config.Facility
	if facility == 0 {
		facility = 16 // local0
	}

	severity := actionSeverity(msg.Action)
	pri := facility*8 + severity

	ts := msg.Timestamp.UTC().Format(time.RFC3339Nano)

	clusterID := msg.ClusterID
	if clusterID == "" {
		clusterID = "system"
	}

	body := FormatAuditBody(msg.UserID, clusterID, msg.ResourceType, msg.ResourceID, msg.Action, msg.Details)

	line := fmt.Sprintf("<%d>1 %s nexara audit - - - %s\n", pri, ts, body)
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
