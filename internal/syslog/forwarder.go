// Package syslog provides RFC 5424 syslog forwarding for audit events.
package syslog

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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

	// enabled mirrors config.Enabled outside the mutex so Forward can check it
	// without ever blocking. Reading it through f.mu would put request handlers
	// behind Configure, which holds the write lock across a 5s dial.
	enabled atomic.Bool

	// queue decouples Forward from the network. Everything past this point runs
	// on the single sender goroutine, which is the only thing that touches conn
	// for writing.
	queue     chan queueItem
	stop      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	dropped   atomic.Uint64
}

// queueItem is either an audit message to send or a flush barrier. A barrier
// carries no message; the sender just closes done when it reaches it, which
// tells Flush that everything queued ahead of it has been written.
type queueItem struct {
	msg  Message
	done chan struct{}
}

// forwardQueueDepth bounds how many audit records may be in flight to the
// collector. Deep enough to absorb a burst of mutations while a slow collector
// catches up, shallow enough that a permanently unreachable one costs bounded
// memory rather than growing without limit.
const forwardQueueDepth = 1024

// NewForwarder creates a new syslog forwarder and starts its sender goroutine.
// Callers must Close it to stop that goroutine.
func NewForwarder(logger *slog.Logger) *Forwarder {
	if logger == nil {
		logger = slog.Default()
	}
	f := &Forwarder{
		logger: logger,
		queue:  make(chan queueItem, forwardQueueDepth),
		stop:   make(chan struct{}),
	}
	f.wg.Add(1)
	go f.run()
	return f
}

// run is the sender goroutine. It owns writing to the connection, so dialling,
// write deadlines and reconnect-and-retry all happen here rather than on a
// request.
func (f *Forwarder) run() {
	defer f.wg.Done()
	for {
		select {
		case item := <-f.queue:
			f.handle(item)
		case <-f.stop:
			// Drain what was already accepted. A record that Forward took
			// responsibility for should not be discarded just because shutdown
			// began, and the queue is bounded so this terminates.
			for {
				select {
				case item := <-f.queue:
					f.handle(item)
				default:
					return
				}
			}
		}
	}
}

func (f *Forwarder) handle(item queueItem) {
	if item.done != nil {
		close(item.done)
		return
	}
	f.send(item.msg)
}

// Dropped returns the number of audit records discarded because the queue was
// full. Non-zero means the collector could not keep up and records were lost —
// the counter is the only remaining evidence that they existed.
func (f *Forwarder) Dropped() uint64 {
	return f.dropped.Load()
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
	f.enabled.Store(cfg.Enabled)

	if !cfg.Enabled {
		return nil
	}

	return f.connectLocked()
}

// Flush blocks until every record already queued has been written, or until ctx
// is done. It is a barrier, not a lock: records enqueued after the call may or
// may not be included.
//
// Callers that need a record to go out over the *current* connection must Flush
// before reconfiguring. UpdateSyslogConfig does exactly that — the notice that
// forwarding is being switched off has to reach the collector being switched
// off, and that is the one moment it can still be told anything. Without the
// barrier the send would race the reconfigure and usually lose.
func (f *Forwarder) Flush(ctx context.Context) {
	done := make(chan struct{})
	// Blocking send, unlike Forward: a caller asking for a barrier is willing
	// to wait for a slot, bounded by ctx.
	select {
	case f.queue <- queueItem{done: done}:
	case <-ctx.Done():
		return
	case <-f.stop:
		return
	}

	select {
	case <-done:
	case <-ctx.Done():
	case <-f.stop:
	}
}

// Config returns the current configuration.
func (f *Forwarder) Config() Config {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.config
}

// Forward hands an audit message to the sender goroutine. It never blocks and
// never touches the network, so it is safe to call on a request path.
//
// It used to dial, write, reconnect and retry inline while holding the
// forwarder's write lock. With the timeouts in this file that is up to ~16
// seconds against an unreachable collector, and because the lock was held
// throughout, every concurrent audited mutation queued behind it — so pointing
// forwarding at a host that silently drops SYNs degraded the whole appliance,
// and a manage:audit holder could do that with one config change.
//
// A full queue drops the record rather than stalling the request. That is the
// deliberate trade: audit rows are already durable in Postgres, and the drop is
// counted and logged, whereas a stall is unbounded damage to unrelated work.
func (f *Forwarder) Forward(msg Message) {
	if !f.enabled.Load() {
		return
	}

	select {
	case <-f.stop:
		return // shutting down; the sender is on its way out
	default:
	}

	select {
	case f.queue <- queueItem{msg: msg}:
	default:
		// Log the first drop and then sparsely: the condition persists for as
		// long as the collector is down, and a line per dropped record would
		// itself become the flood.
		if n := f.dropped.Add(1); n == 1 || n%100 == 0 {
			f.logger.Warn("syslog: forward queue full, dropping audit records",
				"dropped_total", n, "queue_depth", cap(f.queue))
		}
	}
}

// send performs the actual write. Only the sender goroutine calls it, so the
// dial-and-retry cost lands there instead of on a request.
func (f *Forwarder) send(msg Message) {
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

// Close stops the sender goroutine and closes the underlying connection. It
// drains whatever was already queued first, so a clean shutdown does not
// discard records Forward had accepted. Safe to call more than once.
func (f *Forwarder) Close() {
	f.closeOnce.Do(func() {
		close(f.stop)
		f.wg.Wait()

		f.mu.Lock()
		defer f.mu.Unlock()
		f.enabled.Store(false)
		if f.conn != nil {
			_ = f.conn.Close()
			f.conn = nil
		}
	})
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

// sdID is the SD-ID of the structured-data element carrying every audit field.
//
// 32473 is the IANA private enterprise number reserved for documentation and
// examples (RFC 5612). Nexara has no PEN of its own; using the documentation
// one is the conventional choice and keeps the SD-ID globally unambiguous in
// form. An operator who needs a real PEN can rewrite it at the collector.
const sdID = "nexara@32473"

// Caps on what one record may carry. %-escaping expands values — a quote costs
// 2 bytes, a stripped control character 0, a JSON details blob roughly 1.2x —
// so the pre-escape budget is what has to be bounded.
//
// The risk is not memory, it is framing. RFC 6587 non-transparent framing
// delimits records with LF, and a record over a collector's maximum message
// size is either truncated or *split* depending on configuration — rsyslog's
// oversizemsg.input.mode has a split mode. Under split, the tail of an
// over-long details blob becomes an independent record whose entire content the
// caller chose, which reinstates exactly the record forging the escaping below
// removes. Capping the inputs keeps the assembled record comfortably under the
// 8 KiB that collectors commonly default to.
//
// details gets the large budget because it is the only field that legitimately
// carries a payload; the rest are identifiers.
const (
	maxSyslogDetailsLen = 3072
	maxSyslogFieldLen   = 256
)

// truncateRunes bounds s to maxRunes runes, cutting on a rune boundary so the
// result stays valid UTF-8, and marks the cut so a reader can tell a truncated
// value from one that happened to be that long. Mirrors auditTruncate in
// internal/api/handlers.
func truncateRunes(s string, maxRunes int) string {
	if len(s) <= maxRunes { // fast path: byte length bounds rune count
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// escapeSDParam renders a value for an RFC 5424 SD-PARAM.
//
// Two separate jobs, and both are load-bearing:
//
//  1. Control characters are dropped. The spec permits them inside a PARAM-VALUE
//     but LF framing does not — a newline ends the record and starts one the
//     caller wrote in full. Dropping rather than escaping keeps the output
//     unambiguous: there is no escape sequence a reader could mistake for one.
//  2. `\`, `"` and `]` are backslash-escaped, which is the whole of the SD-PARAM
//     escaping rule (RFC 5424 §6.3.3). Unlike the Go-literal quoting this
//     replaces, it is spec-defined, so every conforming parser unescapes it the
//     same way. That is what closes field forging outright: the previous %q form
//     was exact only for readers that honored backslash escapes, and an
//     escape-blind `key="([^"]*)"` extractor — Splunk's default KV mode,
//     Logstash's kv filter — could still be walked out of a field by an embedded
//     quote.
func escapeSDParam(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			// Dropped: see (1) above.
		case r == '\\' || r == '"' || r == ']':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatAuditSD renders the RFC 5424 STRUCTURED-DATA element that both the live
// forwarder and the audit-log syslog export emit. It is shared so the two cannot
// drift — they already had, back when this was a flat key=value body and the
// export quoted two of the six fields.
//
// Every value is bounded and escaped by the rules above, so no caller-influenced
// input can open a field, close the element, or end the record.
func FormatAuditSD(user, cluster, resourceType, resourceID, action, details string) string {
	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(sdID)

	for _, p := range []struct{ name, value string }{
		{"user", truncateRunes(user, maxSyslogFieldLen)},
		{"cluster", truncateRunes(cluster, maxSyslogFieldLen)},
		{"resource_type", truncateRunes(resourceType, maxSyslogFieldLen)},
		{"resource_id", truncateRunes(resourceID, maxSyslogFieldLen)},
		{"action", truncateRunes(action, maxSyslogFieldLen)},
	} {
		b.WriteByte(' ')
		b.WriteString(p.name)
		b.WriteString(`="`)
		b.WriteString(escapeSDParam(p.value))
		b.WriteByte('"')
	}

	// details is the one optional field. An absent key and an empty one are
	// different statements, so a record that carried nothing renders no key.
	if details != "" && details != "{}" {
		b.WriteString(` details="`)
		b.WriteString(escapeSDParam(truncateRunes(details, maxSyslogDetailsLen)))
		b.WriteByte('"')
	}

	b.WriteByte(']')
	return b.String()
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

	sd := FormatAuditSD(msg.UserID, clusterID, msg.ResourceType, msg.ResourceID, msg.Action, msg.Details)

	// <PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA [MSG].
	// The fields now live in STRUCTURED-DATA rather than a free-form MSG, so MSG
	// is omitted — every value a caller can influence is inside the SD element,
	// where the escaping is spec-defined.
	line := fmt.Sprintf("<%d>1 %s nexara audit - - %s\n", pri, ts, sd)
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
