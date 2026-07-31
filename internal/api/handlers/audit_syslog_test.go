package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// queuedRow is one answer syslogDBTX hands back to a QueryRow, either a row or
// an error.
type queuedRow struct {
	setting db.Setting
	err     error
}

// syslogDBTX answers each QueryRow from a queue so the two the update handler
// issues — the read of the current config, then the write — can be given
// different answers. captureDBTX alone replays one row to every QueryRow, which
// cannot express "nothing stored yet, then a successful write", the case that
// separates a first save from an unreadable one.
//
// Everything else, including the audit-row decoding, comes from the embedded
// captureDBTX so these tests assert against the same fake as the settings ones.
type syslogDBTX struct {
	*captureDBTX
	queue []queuedRow
}

func newSyslogDBTX(queue ...queuedRow) *syslogDBTX {
	return &syslogDBTX{captureDBTX: &captureDBTX{}, queue: queue}
}

func (s *syslogDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.record(sql, args)
	if len(s.queue) == 0 {
		return failRow{err: errCaptured}
	}
	q := s.queue[0]
	s.queue = s.queue[1:]
	if q.err != nil {
		return failRow{err: q.err}
	}
	return settingRow{s: q.setting}
}

// failRow replays a specific scan error — pgx.ErrNoRows above all, which is how
// "no config has ever been saved" reaches the handler.
type failRow struct{ err error }

func (r failRow) Scan(...any) error { return r.err }

// storedSyslog is a settings row holding a syslog config, as the read before an
// update finds it.
func storedSyslog(value string) queuedRow {
	return queuedRow{setting: newTestSetting("global", syslogSettingKey, value)}
}

// noSyslogRow is the answer when nothing has ever been saved.
func noSyslogRow() queuedRow { return queuedRow{err: pgx.ErrNoRows} }

// writeAccepted is the row the upsert hands back; the handler discards it, so
// only the absence of an error matters.
func writeAccepted() queuedRow { return storedSyslog(`{}`) }

// newSyslogTestApp wires the real audit handlers over a fake database. The
// publisher is real but Redis-less (Publish is nil-client-safe), so a syslog
// forwarder can be attached to it — which is what
// TestSyslogConfigChangeReachesOutgoingCollector needs.
func newSyslogTestApp(t *testing.T, dbtx db.DBTX, pub *events.Publisher) *fiber.App {
	t.Helper()

	handler := NewAuditHandler(db.New(dbtx), pub)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			c.Locals("role", role)
			c.Locals("user_id", testSettingsUserID)
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	app.Put("/audit-log/syslog-config", handler.UpdateSyslogConfig)
	app.Post("/audit-log/syslog-test", handler.TestSyslog)

	return app
}

func syslogRequest(t *testing.T, app *fiber.App, method, url, body, role string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// syslogSecret is a field name proxsyslog.Config does not have. Any recorded
// row containing it means something marshalled the whole config rather than
// going through syslogAuditConfig — the failure the drift guard exists to catch,
// asserted here at runtime as well.
const syslogSecret = "collector_token"

// assertSyslogAudit checks the fields both syslog audit actions share and
// returns the raw details for the caller's own assertions.
func assertSyslogAudit(t *testing.T, entry db.InsertAuditLogParams, action string) json.RawMessage {
	t.Helper()

	assertAuditEnvelope(t, entry, action, syslogSettingKey)

	if strings.Contains(string(entry.Details), syslogSecret) {
		t.Errorf("an unclassified config field reached the audit details: %s", entry.Details)
	}
	return entry.Details
}

// TestUpdateSyslogConfigAudits covers the record behind PUT
// /api/v1/audit-log/syslog-config. Redirecting the audit stream to a collector
// of the caller's choosing, or setting enabled:false to stop forwarding
// altogether, is precisely what an audit log exists to capture — and since
// commit 89e2b0e reserved the key from the generic settings endpoints, this
// handler is the only way either happens.
//
// Both ends of the change are recorded. The settings row holds only the new
// value once the write lands, so an entry naming just that cannot answer the
// question a reviewer actually has: where was it going before.
func TestUpdateSyslogConfigAudits(t *testing.T) {
	// Carries a field proxsyslog.Config does not have. Decoding into the config
	// drops it, so it can only reach the audit row if something recorded the
	// stored value verbatim instead of going through syslogAuditConfig — which
	// is exactly how a credential added to the forwarder would leak.
	const storedTLS = `{"enabled":true,"host":"siem-a.internal","port":6514,"protocol":"tls",` +
		`"facility":16,"` + syslogSecret + `":"s3cr3t-sentinel"}`

	tests := []struct {
		name       string
		stored     queuedRow
		body       string
		role       string
		wantStatus int
		audited    bool
		wantPrev   *syslogAuditConfig
		wantUnavl  bool
		wantNew    syslogAuditConfig
	}{
		{
			name:   "redirect to another collector records both ends",
			stored: storedSyslog(storedTLS),
			body:   `{"enabled":true,"host":"attacker.example","port":514,"protocol":"udp","facility":16}`,
			role:   "admin", wantStatus: http.StatusOK, audited: true,
			wantPrev: &syslogAuditConfig{
				Enabled: true, Host: "siem-a.internal", Port: 6514, Protocol: "tls", Facility: 16,
			},
			wantNew: syslogAuditConfig{
				Enabled: true, Host: "attacker.example", Port: 514, Protocol: "udp", Facility: 16,
			},
		},
		{
			// The quietest way to blind the audit trail, and the one that
			// previously left nothing behind at all.
			name:   "disabling forwarding is recorded",
			stored: storedSyslog(storedTLS),
			body:   `{"enabled":false}`,
			role:   "admin", wantStatus: http.StatusOK, audited: true,
			wantPrev: &syslogAuditConfig{
				Enabled: true, Host: "siem-a.internal", Port: 6514, Protocol: "tls", Facility: 16,
			},
			// Defaults the handler fills in for a disabled config.
			wantNew: syslogAuditConfig{Port: 514, Protocol: "udp", Facility: 16},
		},
		{
			// previous null with no marker: nothing was configured before.
			name:   "first save records no previous",
			stored: noSyslogRow(),
			body:   `{"enabled":true,"host":"siem-a.internal","port":6514,"protocol":"tls","facility":16}`,
			role:   "admin", wantStatus: http.StatusOK, audited: true,
			wantNew: syslogAuditConfig{
				Enabled: true, Host: "siem-a.internal", Port: 6514, Protocol: "tls", Facility: 16,
			},
		},
		{
			// A stored config that will not load is not the same as none, and
			// the record says which rather than inventing a "before".
			name:   "unreadable stored config is marked, not guessed",
			stored: storedSyslog(`"not-a-config"`),
			body:   `{"enabled":true,"host":"siem-b.internal","port":514,"protocol":"udp","facility":16}`,
			role:   "admin", wantStatus: http.StatusOK, audited: true,
			wantUnavl: true,
			wantNew: syslogAuditConfig{
				Enabled: true, Host: "siem-b.internal", Port: 514, Protocol: "udp", Facility: 16,
			},
		},

		// Nothing was written, so nothing is recorded — the log says what
		// happened, not what was attempted.
		{
			name:   "refused for lacking manage:audit",
			stored: storedSyslog(storedTLS),
			body:   `{"enabled":true,"host":"attacker.example","port":514,"protocol":"udp"}`,
			role:   "viewer", wantStatus: http.StatusForbidden,
		},
		{
			name:   "rejected by validation",
			stored: storedSyslog(storedTLS),
			body:   `{"enabled":true,"host":"","port":514,"protocol":"udp"}`,
			role:   "admin", wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := newSyslogDBTX(tt.stored, writeAccepted())
			app := newSyslogTestApp(t, capture, nil)

			resp := syslogRequest(t, app, http.MethodPut, "/audit-log/syslog-config", tt.body, tt.role)
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, tt.wantStatus, body)
			}

			entries := capture.auditInserts(t)
			if !tt.audited {
				if len(entries) != 0 {
					t.Fatalf("wrote %d audit row(s) for a change that never landed: %+v", len(entries), entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("wrote %d audit rows, want exactly 1: %+v", len(entries), entries)
			}

			raw := assertSyslogAudit(t, entries[0], syslogUpdatedAction)
			var got syslogAuditDetails
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode details %s: %v", raw, err)
			}

			switch {
			case tt.wantPrev == nil && got.Previous != nil:
				t.Errorf("details.previous = %+v, want null", *got.Previous)
			case tt.wantPrev != nil && got.Previous == nil:
				t.Errorf("details.previous = null, want %+v — the record cannot say what was replaced", *tt.wantPrev)
			case tt.wantPrev != nil && *got.Previous != *tt.wantPrev:
				t.Errorf("details.previous = %+v, want %+v", *got.Previous, *tt.wantPrev)
			}
			if got.PreviousUnavailable != tt.wantUnavl {
				t.Errorf("details.previous_unavailable = %v, want %v", got.PreviousUnavailable, tt.wantUnavl)
			}
			if got.New != tt.wantNew {
				t.Errorf("details.new = %+v, want %+v", got.New, tt.wantNew)
			}
		})
	}
}

// TestUpdateSyslogConfigAuditTrailsTheWrite pins the ordering: the audit call
// runs after the settings write returns, so a write the database rejected
// records nothing.
//
// The refused cases above cannot show this. They stop at the permission gate or
// at validation, before any query, so they would pass just as happily with the
// audit call moved above the write — at which point the log would assert
// redirections that never took effect.
func TestUpdateSyslogConfigAuditTrailsTheWrite(t *testing.T) {
	capture := newSyslogDBTX(
		noSyslogRow(),
		queuedRow{err: errors.New("connection reset by peer")},
	)
	app := newSyslogTestApp(t, capture, nil)

	resp := syslogRequest(t, app, http.MethodPut, "/audit-log/syslog-config",
		`{"enabled":true,"host":"siem-a.internal","port":514,"protocol":"udp"}`, "admin")
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusInternalServerError, body)
	}

	// Without this the test could pass for the wrong reason. A request stopped
	// before the upsert records nothing either, and the handler now issues a
	// read ahead of the write, so "some query ran" no longer distinguishes the
	// two — the write itself has to be the thing that was reached and failed.
	if !capture.ranStatement("INSERT INTO settings") {
		t.Fatalf("the settings write never ran (%d queries issued) — this proves nothing about ordering",
			len(capture.calls))
	}
	if entries := capture.auditInserts(t); len(entries) != 0 {
		t.Errorf("wrote %d audit row(s) for a save the database rejected: %+v", len(entries), entries)
	}
}

// TestSyslogConfigChangeReachesOutgoingCollector covers the other half of the
// placement: the audit call sits before the live forwarder is reconfigured, so
// the entry goes out over the connection still in force. The collector being
// switched off is told that it was — which is the one moment it can still be
// told anything.
//
// Move the AuditLog call below Configure and this fails: the forwarder is
// disabled by then and drops the message, leaving the notice nowhere.
func TestSyslogConfigChangeReachesOutgoingCollector(t *testing.T) {
	collector, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the collector: %v", err)
	}
	defer func() { _ = collector.Close() }()

	port := collector.LocalAddr().(*net.UDPAddr).Port

	fwd := proxsyslog.NewForwarder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer fwd.Close()
	if err := fwd.Configure(proxsyslog.Config{
		Enabled: true, Host: "127.0.0.1", Port: port, Protocol: "udp", Facility: 16,
	}); err != nil {
		t.Fatalf("configure the outgoing forwarder: %v", err)
	}

	pub := events.NewPublisher(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pub.SetSyslogForwarder(fwd)

	capture := newSyslogDBTX(
		storedSyslog(`{"enabled":true,"host":"127.0.0.1","port":`+
			strconv.Itoa(port)+`,"protocol":"udp","facility":16}`),
		writeAccepted(),
	)
	app := newSyslogTestApp(t, capture, pub)

	resp := syslogRequest(t, app, http.MethodPut, "/audit-log/syslog-config", `{"enabled":false}`, "admin")
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, body)
	}

	if err := collector.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := collector.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the collector being switched off received nothing: %v\n"+
			"\tThe audit entry must be written before the forwarder is reconfigured, "+
			"or a disable leaves no notice on the outgoing connection.", err)
	}

	line := string(buf[:n])
	if !strings.Contains(line, syslogUpdatedAction) {
		t.Errorf("collector received %q, want a %s entry", line, syslogUpdatedAction)
	}
}

// TestSyslogProbeAudits covers POST /api/v1/audit-log/syslog-test, which opens
// an outbound connection to a host and port the caller names and writes to it.
// That is worth a record on its own account: a run of probes is how the network
// around the appliance would be swept, and the failures are as informative to
// whoever ran them as the successes.
func TestSyslogProbeAudits(t *testing.T) {
	collector, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the collector: %v", err)
	}
	defer func() { _ = collector.Close() }()

	reachable := collector.LocalAddr().(*net.UDPAddr).Port

	tests := []struct {
		name        string
		body        string
		role        string
		wantStatus  int
		audited     bool
		wantSuccess bool
		wantTarget  syslogAuditConfig
	}{
		{
			name: "a probe that connects is recorded",
			body: `{"host":"127.0.0.1","port":` + strconv.Itoa(reachable) + `,"protocol":"udp","facility":16}`,
			role: "admin", wantStatus: http.StatusOK, audited: true, wantSuccess: true,
			wantTarget: syslogAuditConfig{Host: "127.0.0.1", Port: reachable, Protocol: "udp", Facility: 16},
		},
		{
			// A failed probe still names the host that was reached for. The
			// protocol is deliberately one the dialler rejects: unlike the
			// update endpoint, this one does not validate it, so the failure is
			// caller-reachable and needs no network to reproduce.
			name: "a probe that fails is recorded with the reason",
			body: `{"host":"unreachable.internal","port":9999,"protocol":"sctp","facility":16}`,
			role: "admin", wantStatus: http.StatusBadRequest, audited: true, wantSuccess: false,
			wantTarget: syslogAuditConfig{Host: "unreachable.internal", Port: 9999, Protocol: "sctp", Facility: 16},
		},

		// No connection was attempted, so there is nothing to record.
		{
			name: "refused for lacking manage:audit",
			body: `{"host":"127.0.0.1","port":514,"protocol":"udp"}`,
			role: "viewer", wantStatus: http.StatusForbidden,
		},
		{
			name: "rejected by validation",
			body: `{"host":"","port":514,"protocol":"udp"}`,
			role: "admin", wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := newSyslogDBTX()
			app := newSyslogTestApp(t, capture, nil)

			resp := syslogRequest(t, app, http.MethodPost, "/audit-log/syslog-test", tt.body, tt.role)
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, tt.wantStatus, body)
			}

			entries := capture.auditInserts(t)
			if !tt.audited {
				if len(entries) != 0 {
					t.Fatalf("wrote %d audit row(s) for a probe that never ran: %+v", len(entries), entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("wrote %d audit rows, want exactly 1: %+v", len(entries), entries)
			}

			raw := assertSyslogAudit(t, entries[0], syslogTestedAction)
			var got syslogTestAuditDetails
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode details %s: %v", raw, err)
			}

			if got.Target != tt.wantTarget {
				t.Errorf("details.target = %+v, want %+v", got.Target, tt.wantTarget)
			}
			if got.Success != tt.wantSuccess {
				t.Errorf("details.success = %v, want %v", got.Success, tt.wantSuccess)
			}
			if tt.wantSuccess && got.Error != "" {
				t.Errorf("details.error = %q, want empty on a probe that connected", got.Error)
			}
			if !tt.wantSuccess && got.Error == "" {
				t.Error("details.error is empty — a failed probe records no reason")
			}
		})
	}
}

// TestSyslogAuditBoundsCallerStrings covers the caps on what a caller can push
// into an audit row. Neither endpoint bounds the host it accepts, and the
// probe's error text wraps the dialler's message around that same host, so
// without the caps one request writes a body-sized string into audit_log — and
// the probe can be repeated as often as the caller likes.
func TestSyslogAuditBoundsCallerStrings(t *testing.T) {
	host := strings.Repeat("日", 4000) + ".internal"

	capture := newSyslogDBTX()
	app := newSyslogTestApp(t, capture, nil)

	body, _ := json.Marshal(map[string]any{"host": host, "port": 9999, "protocol": "sctp"})
	resp := syslogRequest(t, app, http.MethodPost, "/audit-log/syslog-test", string(body), "admin")
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	entries := capture.auditInserts(t)
	if len(entries) != 1 {
		t.Fatalf("wrote %d audit rows, want exactly 1: %+v", len(entries), entries)
	}

	var got syslogTestAuditDetails
	if err := json.Unmarshal(entries[0].Details, &got); err != nil {
		t.Fatalf("decode details: %v", err)
	}

	// Both bounds are asserted from below as well. An upper bound alone is
	// satisfied by the empty string, so dropping the field entirely — or
	// renaming its json tag — would pass while recording nothing.
	assertTruncated(t, "details.target.host", got.Target.Host, maxSyslogAuditValueLen)
	assertTruncated(t, "details.error", got.Error, maxSyslogAuditErrorLen)

	if !utf8.ValidString(string(entries[0].Details)) {
		t.Errorf("the recorded details are not valid UTF-8: %q", entries[0].Details)
	}
}

// assertTruncated checks a field that auditTruncate should have cut: capped at
// maxRunes plus the ellipsis, still carrying the marker, and still non-empty.
func assertTruncated(t *testing.T, field, got string, maxRunes int) {
	t.Helper()

	switch n := utf8.RuneCountInString(got); {
	case n == 0:
		t.Errorf("%s is empty — the value never reached the audit row", field)
	case n > maxRunes+1: // maxRunes kept plus the ellipsis
		t.Errorf("%s kept %d runes, want at most %d", field, n, maxRunes+1)
	case !strings.HasSuffix(got, "…"):
		t.Errorf("%s = %q, want a truncation marker — it was cut without saying so", field, got)
	}
}

// syslogConfigFieldsOmittedFromAudit lists proxsyslog.Config fields that
// deliberately do not appear in a syslog audit detail, each with the reason.
// Empty today: every field the forwarder carries describes where the audit
// stream goes or how it is protected, and none of them is a credential.
//
// It exists so that omitting a field is a decision someone wrote down.
var syslogConfigFieldsOmittedFromAudit = map[string]string{}

// TestGuard_SyslogAuditFieldsClassified is the drift guard behind
// syslogAuditConfig. It fails when proxsyslog.Config gains a field that the
// audit detail neither records nor lists as deliberately omitted.
//
// Both directions are enforced, and they guard against opposite mistakes.
// Recording a field nobody classified is how a credential added to the
// forwarder — a TLS client key, a collector token — would start being copied
// into audit_log, which has a wider read audience than the settings row it came
// from. Keeping a field the config no longer has is how the record quietly
// starts reporting a zero value as fact.
func TestGuard_SyslogAuditFieldsClassified(t *testing.T) {
	recorded := jsonFieldNames(t, reflect.TypeOf(syslogAuditConfig{}))
	source := jsonFieldNames(t, reflect.TypeOf(proxsyslog.Config{}))

	for name := range source {
		if recorded[name] || syslogConfigFieldsOmittedFromAudit[name] != "" {
			continue
		}
		t.Errorf("proxsyslog.Config field %q appears in neither syslogAuditConfig nor "+
			"syslogConfigFieldsOmittedFromAudit.\n"+
			"\tDecide whether a change to it belongs in the audit log. If it does, add it to "+
			"syslogAuditConfig (internal/api/handlers/audit.go); if it does not — and anything "+
			"credential-bearing does not, audit_log is read more widely than the settings row — "+
			"record why in syslogConfigFieldsOmittedFromAudit.", name)
	}

	for name := range recorded {
		if !source[name] {
			t.Errorf("syslogAuditConfig records %q, which proxsyslog.Config no longer has — "+
				"the audit detail would report a zero value as if it were the setting.", name)
		}
	}
}

// jsonFieldNames indexes a struct's exported fields by the name they marshal
// under, which is what an audit row is actually read by.
//
// It recurses into nested structs and refuses embedded ones, because the guard
// above compares names and both shapes would defeat that. A nested
// `TLS TLSOptions `json:"tls"“ presents as the single name "tls" — adding a
// field of the same name to syslogAuditConfig would satisfy the guard while
// inlining every field underneath, a client key included. An embedded struct is
// worse: its name is the type's, which never appears in the JSON at all, so two
// structs embedding the same type would match on a name neither of them
// marshals.
func jsonFieldNames(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()

	names := map[string]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, prefix string) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			if f.Anonymous {
				t.Fatalf("%s embeds %s: give it a name and a json tag, "+
					"or this guard compares names that never appear in the recorded JSON",
					typ, f.Type)
			}

			name := f.Name
			if tag := f.Tag.Get("json"); tag != "" {
				if tag == "-" {
					continue
				}
				name = strings.Split(tag, ",")[0]
			}
			name = prefix + name

			if ft := f.Type; ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Time{}) {
				walk(ft, name+".")
				continue
			}
			names[name] = true
		}
	}
	walk(typ, "")
	return names
}
