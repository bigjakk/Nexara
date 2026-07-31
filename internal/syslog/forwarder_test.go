package syslog

import (
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// TestFormatAuditBodyContainsHostileValues is the regression lock behind the
// quoting in FormatAuditBody. Both formatters used to interpolate values raw,
// and audit_log.details and resource_id are populated from request data by many
// handlers — so the strings below are reachable by a caller, not hypothetical.
//
// The two properties asserted are the two the doc comment promises, and they
// are deliberately checked separately: control characters can never escape
// (true for any collector), and spaces stay inside the quoted field (true for a
// quote-aware key=value parser).
func TestFormatAuditBodyContainsHostileValues(t *testing.T) {
	tests := []struct {
		name string
		// which argument carries the hostile value; user defaults to "u1"
		user       string
		resourceID string
		details    string
		// the exact rendering the value must land in
		wantField string
	}{
		{
			// exportSyslog sources this from a.UserDisplayName — a profile
			// field the account holder edits — so before the quoting this was
			// a live forging vector reachable by any authenticated user, not
			// just by whoever can influence a resource id.
			name:      "hostile display name cannot open a new field",
			user:      `x" action="login`,
			wantField: `user="x\" action=\"login"`,
		},
		{
			name:       "space in resource_id cannot open a new field",
			resourceID: `x action=login user=root`,
			wantField:  `resource_id="x action=login user=root"`,
		},
		{
			name:       "newline in resource_id cannot start a new record",
			resourceID: "x\n<134>1 2026-01-01T00:00:00Z nexara audit - - - user=\"root\"",
			wantField:  `resource_id="x\n<134>1 2026-01-01T00:00:00Z nexara audit - - - user=\"root\""`,
		},
		{
			name:      "newline in details cannot start a new record",
			details:   "{\"note\":\"a\nb\"}",
			wantField: `details="{\"note\":\"a\nb\"}"`,
		},
		{
			name:       "carriage return is escaped too",
			resourceID: "x\r\ny",
			wantField:  `resource_id="x\r\ny"`,
		},
		{
			name:       "a quote cannot close the field early",
			resourceID: `x" action="login`,
			wantField:  `resource_id="x\" action=\"login"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := tt.user
			if user == "" {
				user = "u1"
			}
			body := FormatAuditBody(user, "c1", "vm", tt.resourceID, "start", tt.details)

			if !strings.Contains(body, tt.wantField) {
				t.Errorf("body did not contain the hostile value as one quoted field.\n got: %s\nwant substring: %s",
					body, tt.wantField)
			}

			// The stronger of the two guarantees, and the one that holds no
			// matter how the collector parses: a record is a line, so a raw
			// newline or carriage return anywhere in the body means the caller
			// can append records of their own choosing.
			if strings.ContainsAny(body, "\n\r") {
				t.Errorf("body carries a raw newline or carriage return, so a caller can forge whole records: %q", body)
			}
		})
	}
}

// TestFormatAuditBodyRendersOrdinaryValues pins the ordinary rendering. Without
// it the test above is satisfied by a function that mangles every value beyond
// recognition, which would make the audit stream useless in the ordinary case
// this feature exists for.
func TestFormatAuditBodyRendersOrdinaryValues(t *testing.T) {
	body := FormatAuditBody("alice@example.com", "prod", "vm", "100", "vm_start",
		`{"upid":"UPID:pve1:0001","node":"pve1"}`)

	want := `user="alice@example.com" cluster="prod" resource_type="vm" resource_id="100" action="vm_start" ` +
		`details="{\"upid\":\"UPID:pve1:0001\",\"node\":\"pve1\"}"`
	if body != want {
		t.Errorf("body  = %s\nwant  = %s", body, want)
	}
}

// TestFormatAuditBodyOmitsEmptyDetails covers the one conditional field. An
// empty or {} details renders no key at all rather than an empty one, which is
// what both formatters did before sharing this helper.
func TestFormatAuditBodyOmitsEmptyDetails(t *testing.T) {
	for _, details := range []string{"", "{}"} {
		body := FormatAuditBody("u1", "c1", "vm", "100", "vm_start", details)
		if strings.Contains(body, "details=") {
			t.Errorf("details=%q rendered a details key: %s", details, body)
		}
	}
}

// TestFormatRFC5424UsesSharedBody checks that the live forwarder actually
// routes through FormatAuditBody. The helper being correct buys nothing if the
// forwarder still formats its own line — which is the drift that left the
// export quoting two fields and the forwarder none.
func TestFormatRFC5424UsesSharedBody(t *testing.T) {
	f := NewForwarder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer f.Close() // NewForwarder starts the sender goroutine
	f.config = Config{Facility: 16}

	line := string(f.formatRFC5424(Message{
		Timestamp:    time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		UserID:       "u1",
		ClusterID:    "", // exercises the "system" fallback
		ResourceType: "vm",
		ResourceID:   `x action=login`,
		Action:       "vm_start",
	}))

	if !strings.Contains(line, `resource_id="x action=login"`) {
		t.Errorf("forwarder line does not carry the shared quoted rendering: %s", line)
	}
	if !strings.Contains(line, `cluster="system"`) {
		t.Errorf("forwarder line lost the empty-cluster fallback: %s", line)
	}
	// One trailing newline terminating the record, and no other.
	if n := strings.Count(line, "\n"); n != 1 || !strings.HasSuffix(line, "\n") {
		t.Errorf("line carries %d newlines, want exactly one at the end: %q", n, line)
	}
}

// TestForwardShedsInsteadOfBlocking is the regression lock behind making
// Forward asynchronous. It builds a forwarder with no sender goroutine, so
// nothing ever drains the queue — the steady state of a collector that has
// stopped answering.
//
// Before this change Forward dialled, wrote, reconnected and retried inline
// while holding the forwarder's write lock: up to ~16 seconds per call against
// an unreachable host, serialized, on the request path. The property that
// matters is that a hopelessly backed-up forwarder costs a request nothing.
func TestForwardShedsInsteadOfBlocking(t *testing.T) {
	const depth = 4
	f := &Forwarder{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		queue:  make(chan queueItem, depth),
		stop:   make(chan struct{}),
	}
	f.enabled.Store(true)

	const sends = 50
	start := time.Now()
	for i := 0; i < sends; i++ {
		f.Forward(Message{Action: "vm_start", ResourceID: "100"})
	}
	elapsed := time.Since(start)

	// Generous bound: the point is orders of magnitude, not a benchmark. The
	// old code would have spent minutes here.
	if elapsed > 2*time.Second {
		t.Errorf("%d Forward calls took %v with nothing draining — Forward is still blocking",
			sends, elapsed)
	}
	if got, want := f.Dropped(), uint64(sends-depth); got != want {
		t.Errorf("Dropped() = %d, want %d (queue depth %d) — shed records must be counted, "+
			"the counter is the only evidence they existed", got, want, depth)
	}
	if got := len(f.queue); got != depth {
		t.Errorf("queue holds %d, want %d", got, depth)
	}
}

// TestForwardIgnoredWhenDisabled pins the cheap early exit. Without it a
// disabled forwarder would still consume queue slots and count drops.
func TestForwardIgnoredWhenDisabled(t *testing.T) {
	f := &Forwarder{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		queue:  make(chan queueItem, 1),
		stop:   make(chan struct{}),
	}
	// enabled defaults to false.
	for i := 0; i < 10; i++ {
		f.Forward(Message{Action: "vm_start"})
	}
	if got := len(f.queue); got != 0 {
		t.Errorf("queued %d messages while disabled, want 0", got)
	}
	if got := f.Dropped(); got != 0 {
		t.Errorf("Dropped() = %d while disabled, want 0 — a disabled forwarder discards nothing", got)
	}
}

// TestCloseDrainsQueuedRecords covers the shutdown path. Forward reports
// success to its caller the moment a record is queued, so discarding the queue
// on Close would lose records the application already treated as forwarded.
func TestCloseDrainsQueuedRecords(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = conn.Close() }()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	f := NewForwarder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := f.Configure(Config{
		Enabled: true, Host: "127.0.0.1", Port: port, Protocol: "udp", Facility: 16,
	}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	const sends = 5
	for i := 0; i < sends; i++ {
		f.Forward(Message{Action: "vm_start", ResourceID: "100"})
	}
	f.Close()
	f.Close() // idempotent

	if got := f.Dropped(); got != 0 {
		t.Fatalf("dropped %d records that fit in the queue", got)
	}
	for i := 0; i < sends; i++ {
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		buf := make([]byte, 4096)
		if _, _, err := conn.ReadFrom(buf); err != nil {
			t.Fatalf("record %d of %d never arrived: %v\n"+
				"\tClose must drain what Forward already accepted.", i+1, sends, err)
		}
	}
}
