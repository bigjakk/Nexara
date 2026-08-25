package syslog

import (
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// TestFormatAuditSDContainsHostileValues is the regression lock behind the
// SD-PARAM escaping. Both formatters once interpolated values raw, and
// audit_log.details and resource_id are populated from request data by many
// handlers — so the strings below are reachable by a caller, not hypothetical.
//
// Two distinct properties are asserted, because they close different attacks:
// control characters are removed (a record cannot be ended or forged), and the
// three SD metacharacters are escaped (a value cannot leave its field or close
// the element).
func TestFormatAuditSDContainsHostileValues(t *testing.T) {
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
			// field the account holder edits — so this is reachable by any
			// authenticated user, not just by whoever can influence a
			// resource id.
			name:      "hostile display name cannot open a new field",
			user:      `x" action="login`,
			wantField: `user="x\" action=\"login"`,
		},
		{
			name:       "a space is harmless inside a quoted SD-PARAM",
			resourceID: `x action=login user=root`,
			wantField:  `resource_id="x action=login user=root"`,
		},
		{
			// The escaping is spec-defined here, unlike the Go-literal quoting
			// this replaced, so an escape-blind extractor cannot be walked out
			// of the field by an embedded quote.
			name:       "a quote cannot close the field early",
			resourceID: `x" action="login`,
			wantField:  `resource_id="x\" action=\"login"`,
		},
		{
			// ] is the one metacharacter unique to SD: unescaped it would end
			// the whole element, and everything after it would be read as MSG.
			name:       "a bracket cannot close the SD element",
			resourceID: `x] [nexara@32473 action="forged"`,
			wantField:  `resource_id="x\] [nexara@32473 action=\"forged\""`,
		},
		{
			name:       "a backslash cannot escape the escaping",
			resourceID: `x\" y`,
			wantField:  `resource_id="x\\\" y"`,
		},
		{
			// Dropped rather than escaped: LF framing means a newline ends the
			// record regardless of what the SD grammar permits.
			name:       "newline in resource_id is removed, not escaped",
			resourceID: "x\n<134>1 2026-01-01T00:00:00Z nexara audit - - -",
			wantField:  `resource_id="x<134>1 2026-01-01T00:00:00Z nexara audit - - -"`,
		},
		{
			name:      "newline in details is removed",
			details:   "{\"note\":\"a\nb\"}",
			wantField: `details="{\"note\":\"ab\"}"`,
		},
		{
			name:       "carriage return is removed too",
			resourceID: "x\r\ny",
			wantField:  `resource_id="xy"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := tt.user
			if user == "" {
				user = "u1"
			}
			sd := FormatAuditSD(AuditSD{User: user, Cluster: "c1", ResourceType: "vm", ResourceID: tt.resourceID, Action: "start", Details: tt.details})

			if !strings.Contains(sd, tt.wantField) {
				t.Errorf("SD did not contain the hostile value as one escaped param.\n got: %s\nwant substring: %s",
					sd, tt.wantField)
			}

			// The unconditional invariant: a record is a line, so a raw newline
			// or carriage return anywhere means the caller can append records of
			// their own choosing.
			if strings.ContainsAny(sd, "\n\r") {
				t.Errorf("SD carries a raw newline or carriage return, so a caller can forge whole records: %q", sd)
			}
			// The element must be exactly one balanced SD element.
			if !strings.HasPrefix(sd, "["+sdID+" ") || !strings.HasSuffix(sd, "]") {
				t.Errorf("SD is not a single well-formed element: %s", sd)
			}
		})
	}
}

// TestFormatAuditSDRendersOrdinaryValues pins the ordinary rendering. Without
// it the test above is satisfied by a function that mangles every value beyond
// recognition, which would make the audit stream useless in the ordinary case
// the feature exists for.
func TestFormatAuditSDRendersOrdinaryValues(t *testing.T) {
	sd := FormatAuditSD(AuditSD{
		User: "alice@example.com", Cluster: "prod", ResourceType: "vm", ResourceID: "100",
		Action: "vm_start", Details: `{"upid":"UPID:pve1:0001","node":"pve1"}`,
	})

	want := `[nexara@32473 user="alice@example.com" cluster="prod" resource_type="vm" ` +
		`resource_id="100" action="vm_start" ` +
		`details="{\"upid\":\"UPID:pve1:0001\",\"node\":\"pve1\"}"]`
	if sd != want {
		t.Errorf("sd   = %s\nwant = %s", sd, want)
	}
}

// TestFormatAuditSDOmitsEmptyDetails covers the one conditional param. An
// absent key and an empty one are different statements about what the record
// carried.
func TestFormatAuditSDOmitsEmptyDetails(t *testing.T) {
	for _, details := range []string{"", "{}"} {
		sd := FormatAuditSD(AuditSD{User: "u1", Cluster: "c1", ResourceType: "vm", ResourceID: "100", Action: "vm_start", Details: details})
		if strings.Contains(sd, "details=") {
			t.Errorf("details=%q rendered a details param: %s", details, sd)
		}
	}
}

// TestFormatAuditSDGuestIdentity covers the two params added for SIEM rules
// keyed on a guest. Both properties matter to a decoder: they are APPENDED
// (after details), so every param an existing decoder already matches keeps its
// position, and they are OMITTED rather than rendered empty, so "no guest" is
// distinguishable from "guest unknown".
func TestFormatAuditSDGuestIdentity(t *testing.T) {
	t.Run("appended after details, in order", func(t *testing.T) {
		sd := FormatAuditSD(AuditSD{
			User: "alice", Cluster: "prod", ResourceType: "vm", ResourceID: "uuid-1",
			Action: "destroy", VMID: "121", ResourceName: "Veeam13-appliance02",
			Details: `{"node":"HV03"}`,
		})
		want := `[nexara@32473 user="alice" cluster="prod" resource_type="vm" ` +
			`resource_id="uuid-1" action="destroy" details="{\"node\":\"HV03\"}" ` +
			`vmid="121" resource_name="Veeam13-appliance02"]`
		if sd != want {
			t.Errorf("sd   = %s\nwant = %s", sd, want)
		}
	})

	t.Run("omitted when the entry names no guest", func(t *testing.T) {
		sd := FormatAuditSD(AuditSD{
			User: "alice", Cluster: "prod", ResourceType: "setting",
			ResourceID: "branding.app_title", Action: "setting_updated",
		})
		if strings.Contains(sd, "vmid=") || strings.Contains(sd, "resource_name=") {
			t.Errorf("a guest-less entry rendered a guest param: %s", sd)
		}
	})

	t.Run("escaped and bounded like every other param", func(t *testing.T) {
		// A VM name is caller-chosen, so it is on the reachable side of the
		// escaping boundary — same as resource_id.
		sd := FormatAuditSD(AuditSD{
			User: "u1", Cluster: "c1", ResourceType: "vm", ResourceID: "100", Action: "start",
			ResourceName: "x\" action=\"login] [nexara@32473 forged=\"1",
		})
		if !strings.Contains(sd, `resource_name="x\" action=\"login\] [nexara@32473 forged=\"1"`) {
			t.Errorf("resource_name was not escaped as one SD param: %s", sd)
		}
		// The property that matters is not how many "[" the value contains —
		// "[" is not an SD metacharacter — but that the only UNESCAPED "]" is
		// the one closing the element.
		if idx := firstUnescapedBracket(sd); idx != len(sd)-1 {
			t.Errorf("resource_name closed the SD element early at %d: %s", idx, sd)
		}

		long := FormatAuditSD(AuditSD{
			User: "u1", Cluster: "c1", ResourceType: "vm", ResourceID: "100", Action: "start",
			ResourceName: strings.Repeat("日", 4000),
		})
		if !strings.Contains(long, "…") {
			t.Error("resource_name was not truncated")
		}
		if !utf8ValidString(long) {
			t.Error("truncation split a multi-byte rune in resource_name")
		}
	})
}

// TestFormatAuditSDBoundsValues covers the length caps. They exist for framing,
// not memory: a record over a collector's maximum message size is truncated or
// split, and a split tail is an independent record whose content the caller
// chose — which is the record forging the escaping otherwise closes.
func TestFormatAuditSDBoundsValues(t *testing.T) {
	// Multi-byte so a naive byte-slice truncation would corrupt UTF-8.
	longDetails := `{"note":"` + strings.Repeat("日", 8000) + `"}`
	longID := strings.Repeat("a", 4000)

	sd := FormatAuditSD(AuditSD{User: "u1", Cluster: "c1", ResourceType: "vm", ResourceID: longID, Action: "start", Details: longDetails})

	if !strings.Contains(sd, "…") {
		t.Error("nothing was truncated, so neither cap applied")
	}
	if n := len([]rune(sd)); n > maxSyslogDetailsLen+maxSyslogFieldLen*6 {
		t.Errorf("assembled SD is %d runes — the caps did not bound the record", n)
	}
	if !utf8ValidString(sd) {
		t.Error("truncation split a multi-byte rune and produced invalid UTF-8")
	}
}

// firstUnescapedBracket returns the index of the first "]" not preceded by a
// backslash, or -1. In a well-formed element that is the final byte.
func firstUnescapedBracket(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ']' {
			continue
		}
		if i > 0 && s[i-1] == '\\' {
			continue
		}
		return i
	}
	return -1
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestFormatRFC5424UsesSharedSD checks that the live forwarder actually routes
// through FormatAuditSD, and that the header still has the right shape. The
// helper being correct buys nothing if the forwarder formats its own line —
// which is the drift that once left the export escaping two fields and the
// forwarder none.
func TestFormatRFC5424UsesSharedSD(t *testing.T) {
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
		t.Errorf("forwarder line does not carry the shared SD rendering: %s", line)
	}
	if !strings.Contains(line, `cluster="system"`) {
		t.Errorf("forwarder line lost the empty-cluster fallback: %s", line)
	}
	// PROCID and MSGID stay NILVALUE; STRUCTURED-DATA now occupies the slot
	// that used to be a third "-".
	if !strings.Contains(line, "nexara audit - - ["+sdID+" ") {
		t.Errorf("header shape is wrong — SD must occupy the STRUCTURED-DATA field: %s", line)
	}
	if n := strings.Count(line, "\n"); n != 1 || !strings.HasSuffix(line, "\n") {
		t.Errorf("line carries %d newlines, want exactly one at the end: %q", n, line)
	}
}

// TestForwardShedsInsteadOfBlocking is the regression lock behind making
// Forward asynchronous. It builds a forwarder with no sender goroutine, so
// nothing ever drains the queue — the steady state of a collector that has
// stopped answering.
//
// Before that change Forward dialled, wrote, reconnected and retried inline
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
