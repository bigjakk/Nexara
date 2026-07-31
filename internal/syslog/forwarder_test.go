package syslog

import (
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
	f := NewForwarder(nil)
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
