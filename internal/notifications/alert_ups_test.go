package notifications

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// From the device rows to a rule's verdict: the worst UPS leads the label, and
// a UPS Nexara cannot read holds a rule only if its last reading could tip it,
// so a dead ups.conf entry neither silences a rule nor keeps its alert firing.
func TestUpsRuleValue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 2, 3, 12, 0, 0, time.UTC)
	row := func(name, status, reach, vars string) db.ListUpsDevicesRow {
		return db.ListUpsDevicesRow{UpsName: name, ServerName: "nut01", ServerEnabled: true, Status: status,
			Reachability: reach, Vars: json.RawMessage(vars),
			LastReadingAt: pgtype.Timestamptz{Time: now.Add(-10 * time.Second), Valid: true}}
	}
	ol := row("ups01", "OL", "ok", `{"battery.charge":"100","battery.runtime":"1440","ups.load":"38"}`)
	ob := row("ups02", "OB DISCHRG", "ok", `{"battery.charge":"52","battery.runtime":"580","ups.load":"82"}`)
	ob2 := row("ups03", "OB", "ok", `{"battery.runtime":"1200"}`)
	gone := row("ups04", "OB", "unreachable", `{"battery.charge":"40"}`) // last seen on battery
	old := row("ups05", "OL", "ok", `{}`)                                // last seen on mains, then nothing
	old.LastReadingAt.Time = now.Add(-10 * time.Minute)
	off := row("ups06", "OB", "unreachable", `{}`)
	off.ServerEnabled = false
	fsd := row("ups07", "FSD OL", "ok", `{}`)
	rb := row("ups08", "OL RB", "ok", `{}`)
	rows := func(r ...db.ListUpsDevicesRow) []db.ListUpsDevicesRow { return r }

	for _, tc := range []struct {
		name, metric, op string
		threshold        float64
		rows             []db.ListUpsDevicesRow
		want             upsRuleOutcome
	}{
		{"on battery: the least runtime leads", MetricUPSOnBattery, ">", 0, rows(ol, ob, ob2),
			upsRuleOutcome{2, "ups02@nut01 is on battery · 52% · 9m 40s left (2 of 3 UPS on battery)", true, true, false}},
		{"FSD on mains is not on battery", MetricUPSOnBattery, ">", 0, rows(fsd),
			upsRuleOutcome{0, "No UPS is on battery (1 watched)", true, false, false}},
		{"what the readable UPSes show stands", MetricUPSOnBattery, ">", 0, rows(ob, gone),
			upsRuleOutcome{1, "ups02@nut01 is on battery · 52% · 9m 40s left · 1 UPS not read", true, true, false}},
		{"an unreadable UPS last seen on battery holds the verdict", MetricUPSOnBattery, ">", 0, rows(ol, gone),
			upsRuleOutcome{hold: true}},
		{"one last seen on mains does not", MetricUPSOnBattery, ">", 0, rows(ol, old),
			upsRuleOutcome{0, "No UPS is on battery (2 watched) · 1 UPS not read", true, false, false}},
		{"a lowest held by its last reading", MetricUPSBatteryCharge, "<", 50, rows(ol, gone),
			upsRuleOutcome{hold: true}},
		{"ups_unreachable counts them, a stale one too", MetricUPSUnreachable, ">", 0, rows(ob, gone, old),
			upsRuleOutcome{2, "2 of 3 UPS cannot be read: ups04@nut01, ups05@nut01", true, true, false}},
		{"a server with polling off is left out", MetricUPSUnreachable, ">", 0, rows(ol, off),
			upsRuleOutcome{0, "Every UPS can be read (1 watched)", true, false, false}},
		{"lowest charge", MetricUPSBatteryCharge, "<", 50, rows(ol, ob),
			upsRuleOutcome{52, "ups02@nut01 · 52% charge (lowest of 2)", true, false, false}},
		{"shortest runtime, in minutes", MetricUPSRuntime, "<", 30, rows(ol, ob2),
			upsRuleOutcome{20, "ups03@nut01 · 20m 00s runtime (shortest of 2)", true, true, false}},
		{"highest load", MetricUPSLoad, ">", 80, rows(ol, ob),
			upsRuleOutcome{82, "ups02@nut01 · 82% load (highest of 2)", true, true, false}},
		{"no UPS reports a load: nothing to judge", MetricUPSLoad, ">", 80, rows(fsd),
			upsRuleOutcome{0, "No UPS reports its load", false, false, false}},
		{"replace battery", MetricUPSReplaceBattery, ">", 0, rows(ol, rb),
			upsRuleOutcome{1, "ups08@nut01 reports its battery needs replacing", true, true, false}},
	} {
		got := upsRuleValue(tc.metric, tc.op, tc.threshold, upsAlertReadings(tc.rows, 10*time.Second, now))
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// A global rule judges every UPS no maintenance window covers; a rule of scope
// ups the one it names, none once that UPS is gone, and holds while a window
// covers it.
func TestUpsRuleRows(t *testing.T) {
	t.Parallel()
	a, b := db.ListUpsDevicesRow{ID: uuid.New(), UpsName: "ups01"}, db.ListUpsDevicesRow{ID: uuid.New(), UpsName: "ups02"}
	underB := []db.MaintenanceWindow{{UpsID: pgtype.UUID{Bytes: b.ID, Valid: true}}}
	for _, tc := range []struct {
		name    string
		rule    db.AlertRule
		windows []db.MaintenanceWindow
		want    []string
		held    bool
	}{
		{"global", db.AlertRule{ScopeType: "global"}, nil, []string{"ups01", "ups02"}, false},
		{"global, one UPS under a window", db.AlertRule{ScopeType: "global"}, underB, []string{"ups01"}, false},
		{"its own UPS", db.AlertRule{ScopeType: "ups", UpsID: pgtype.UUID{Bytes: b.ID, Valid: true}}, nil, []string{"ups02"}, false},
		{"its own UPS, under a window", db.AlertRule{ScopeType: "ups", UpsID: pgtype.UUID{Bytes: b.ID, Valid: true}}, underB, nil, true},
		{"a UPS no longer listed", db.AlertRule{ScopeType: "ups", UpsID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}, nil, nil, false},
		// Its NUT server's node is being rebooted by a rolling update.
		{"unreachable, one down for a rolling update", db.AlertRule{ScopeType: "global", Metric: MetricUPSUnreachable}, nil,
			[]string{"ups01"}, false},
		{"unreachable, its own UPS down for a rolling update", db.AlertRule{ScopeType: "ups", Metric: MetricUPSUnreachable,
			UpsID: pgtype.UUID{Bytes: b.ID, Valid: true}}, nil, nil, true},
	} {
		judged, held := upsRuleRows(tc.rule, []db.ListUpsDevicesRow{a, b}, tc.windows, []uuid.UUID{b.ID})
		if held != tc.held {
			t.Errorf("%s: held = %v, want %v", tc.name, held, tc.held)
		}
		got := make([]string, 0, len(judged))
		for _, r := range judged {
			got = append(got, r.UpsName)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: judges %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A window drops its UPS out of a global rule, but where that UPS alone meets
// the condition the verdict holds: a window opened during an outage neither
// resolves its alert nor raises one.
func TestUpsRuleVerdictHoldsWhatAWindowHides(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 4, 3, 12, 0, 0, time.UTC)
	row := func(name, status string) db.ListUpsDevicesRow {
		return db.ListUpsDevicesRow{ID: uuid.New(), UpsName: name, ServerName: "nut01", ServerEnabled: true, Status: status,
			Reachability: "ok", Vars: json.RawMessage(`{}`), LastReadingAt: pgtype.Timestamptz{Time: now.Add(-10 * time.Second), Valid: true}}
	}
	ol, ob := row("ups01", "OL"), row("ups02", "OB")
	under := func(r db.ListUpsDevicesRow) []db.MaintenanceWindow {
		return []db.MaintenanceWindow{{UpsID: pgtype.UUID{Bytes: r.ID, Valid: true}}}
	}
	global := db.AlertRule{ScopeType: "global", Metric: MetricUPSOnBattery, Operator: ">", Threshold: 0}
	for _, tc := range []struct {
		name      string
		windows   []db.MaintenanceWindow
		hold, met bool
	}{
		{"no window", nil, false, true},
		{"a window on the UPS on battery", under(ob), true, false},
		{"a window on another UPS", under(ol), false, true},
	} {
		got := upsRuleVerdict(global, []db.ListUpsDevicesRow{ol, ob}, tc.windows, nil, 10*time.Second, now)
		if got.hold != tc.hold || got.met != tc.met {
			t.Errorf("%s: hold %v met %v, want hold %v met %v", tc.name, got.hold, got.met, tc.hold, tc.met)
		}
	}
}
