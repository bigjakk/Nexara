package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// exportSyslogLines drives the real exportSyslog over items and returns the
// rendered records.
//
// It goes through the method rather than re-deriving the format, which is the
// whole point: exportSyslog is the side of the RFC 5424 rendering that
// historically drifted from the live forwarder, and until this file it had no
// test at all. FormatAuditSD takes six consecutive string parameters, so
// transposing two of them at the call site compiles cleanly and ships.
func exportSyslogLines(t *testing.T, items []db.ListAuditLogAdvancedRow, visible map[string]bool) []string {
	t.Helper()

	h := NewAuditHandler(nil, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Get("/export", func(c fiber.Ctx) error {
		return h.exportSyslog(c, items, "20260731-120000", visible)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/export", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body: %s", resp.StatusCode, body)
	}
	if len(body) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

// auditRow builds one exportable row with the fields exportSyslog reads.
func auditRow(displayName, clusterName, resourceType, resourceID, action, details string) db.ListAuditLogAdvancedRow {
	r := db.ListAuditLogAdvancedRow{
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(details),
		CreatedAt:    time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		ClusterName:  clusterName,
	}
	r.UserDisplayName.String = displayName
	r.UserDisplayName.Valid = displayName != ""
	return r
}

// TestExportSyslogRendersFieldsInOrder is the transposition guard. Every value
// is distinguishable, so swapping any two arguments at the FormatAuditSD call
// site changes this line.
func TestExportSyslogRendersFieldsInOrder(t *testing.T) {
	lines := exportSyslogLines(t,
		[]db.ListAuditLogAdvancedRow{
			auditRow("alice", "prod", "vm", "100", "vm_start", `{"node":"pve1"}`),
		}, nil)

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}

	const want = `[nexara@32473 user="alice" cluster="prod" resource_type="vm" ` +
		`resource_id="100" action="vm_start" details="{\"node\":\"pve1\"}"]`
	if !strings.Contains(lines[0], want) {
		t.Errorf("rendered SD does not match.\n got: %s\nwant substring: %s", lines[0], want)
	}
	// The RFC 5424 header is still assembled by the caller, not the helper.
	// Two NILVALUEs now, not three: STRUCTURED-DATA occupies the third slot.
	if !strings.HasPrefix(lines[0], "<") || !strings.Contains(lines[0], "nexara audit - - [nexara@32473 ") {
		t.Errorf("line lost its RFC 5424 header, or SD is not in the STRUCTURED-DATA field: %s", lines[0])
	}
}

// TestExportSyslogEscapesHostileValues covers the injection the quoting exists
// for, on the export side. UserDisplayName is a profile field the account
// holder edits, so this is reachable by any authenticated user.
func TestExportSyslogEscapesHostileValues(t *testing.T) {
	lines := exportSyslogLines(t,
		[]db.ListAuditLogAdvancedRow{
			auditRow(`x" action="login`, "prod", "vm", "a b", "vm_start",
				"{\"note\":\"line1\nline2\"}"),
		}, nil)

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 — a value broke record framing: %q", len(lines), lines)
	}
	for _, want := range []string{
		`user="x\" action=\"login"`,
		`resource_id="a b"`,
		// The newline is removed rather than escaped: LF frames records.
		`details="{\"note\":\"line1line2\"}"`,
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("missing %s in: %s", want, lines[0])
		}
	}
}

// TestExportSyslogEmitsOneRecordPerRow is the framing lock. A caller-supplied
// newline that survived unescaped would split one row into two records, and the
// extra record's content is whatever the caller wrote.
func TestExportSyslogEmitsOneRecordPerRow(t *testing.T) {
	rows := []db.ListAuditLogAdvancedRow{
		auditRow("alice", "prod", "vm", "100", "vm_start", `{}`),
		auditRow("mallory", "prod", "vm",
			"x\n<134>1 2026-07-31T12:00:00Z nexara audit - - - user=\"root\" action=\"forged\"",
			"vm_stop", `{}`),
		auditRow("bob", "prod", "vm", "102", "vm_start", `{}`),
	}

	lines := exportSyslogLines(t, rows, nil)
	if len(lines) != len(rows) {
		t.Fatalf("got %d records for %d rows — a value forged records:\n%s",
			len(lines), len(rows), strings.Join(lines, "\n"))
	}
	// The forged text must survive only as an escaped SD-PARAM value, never as
	// a param of its own.
	if strings.Contains(lines[1], `action="forged"`) {
		t.Errorf("forged action escaped its field and became a real param: %s", lines[1])
	}
	if !strings.Contains(lines[1], `action=\"forged\"`) {
		t.Errorf("the hostile value did not survive escaped inside resource_id: %s", lines[1])
	}
}

// TestExportSyslogAppliesDetailRedaction is the authz lock, and the reason this
// file pins the export rather than only the helper. auditDetailsFor is what
// keeps a reserved global setting's details from a caller who lacks the
// permission that owns it; the syslog exporter has to run it per row, exactly
// as the CSV and JSON exporters do. A refactor that hoisted or dropped it would
// leak the value here while the other two formats stayed correct.
func TestExportSyslogAppliesDetailRedaction(t *testing.T) {
	const secret = `{"host":"siem-a.internal","port":6514}`
	row := auditRow("alice", "system", settingResourceType, syslogSettingKey, "setting_updated", secret)

	t.Run("redacted without the owning permission", func(t *testing.T) {
		lines := exportSyslogLines(t, []db.ListAuditLogAdvancedRow{row},
			map[string]bool{syslogSettingKey: false})

		if len(lines) != 1 {
			t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
		}
		if strings.Contains(lines[0], "siem-a.internal") {
			t.Errorf("the syslog destination leaked to a caller without manage:audit: %s", lines[0])
		}
		if !strings.Contains(lines[0], `\"redacted\":true`) {
			t.Errorf("no redaction marker — a dropped details key is indistinguishable "+
				"from a row that carried none: %s", lines[0])
		}
	})

	t.Run("shown with the owning permission", func(t *testing.T) {
		lines := exportSyslogLines(t, []db.ListAuditLogAdvancedRow{row},
			map[string]bool{syslogSettingKey: true})

		if len(lines) != 1 {
			t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
		}
		// Asserted from both sides: a redactor that fired unconditionally would
		// satisfy the case above while making the export useless to an admin.
		if !strings.Contains(lines[0], "siem-a.internal") {
			t.Errorf("details were withheld from a caller who holds manage:audit: %s", lines[0])
		}
	})
}
