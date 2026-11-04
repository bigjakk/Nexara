package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut"
)

// UPS metric names. A NUT server belongs to no cluster, so a rule is GLOBAL —
// it judges every UPS and its alert names the worst one — or of scope ups,
// judging the one UPS it names.
const (
	MetricUPSOnBattery      = "ups_on_battery"
	MetricUPSBatteryCharge  = "ups_battery_charge"
	MetricUPSRuntime        = "ups_runtime"
	MetricUPSLoad           = "ups_load"
	MetricUPSUnreachable    = "ups_unreachable"
	MetricUPSReplaceBattery = "ups_replace_battery"
)

// upsMetrics are the UPS metrics, the one list every reader of them derives
// from (IsUPSMetric): a metric missing from a second copy would miss the
// view:ups gate its alerts need.
var upsMetrics = []string{MetricUPSOnBattery, MetricUPSBatteryCharge, MetricUPSRuntime, MetricUPSLoad,
	MetricUPSUnreachable, MetricUPSReplaceBattery}

// IsUPSMetric reports whether a metric reads the UPS readings. Its rules, of
// any scope, raise alerts that name a UPS and its reading, so they take the
// global view:ups every UPS route takes.
func IsUPSMetric(metric string) bool {
	return slices.Contains(upsMetrics, metric)
}

// SetUpsPollInterval tells the UPS rules how often readings arrive, so they
// judge a reading's age as the Power page does (nut.StaleAfter).
func (e *Engine) SetUpsPollInterval(d time.Duration) { e.upsPollInterval = d }

// evaluateUPSRule handles the ups_* metrics. windows are the maintenance
// windows open now.
func (e *Engine) evaluateUPSRule(ctx context.Context, rule db.AlertRule, windows []db.MaintenanceWindow) error {
	if rule.ScopeType != "global" && rule.ScopeType != "ups" {
		return fmt.Errorf("%s does not support scope_type %q", rule.Metric, rule.ScopeType)
	}
	rows, err := e.queries.ListUpsDevices(ctx)
	if err != nil {
		return fmt.Errorf("ups devices: %w", err)
	}
	var rolling []uuid.UUID
	if rule.Metric == MetricUPSUnreachable {
		if rolling, err = e.queries.ListUpsOnRollingUpdateHosts(ctx); err != nil {
			return fmt.Errorf("ups on rolling update hosts: %w", err)
		}
	}
	out := upsRuleVerdict(rule, rows, windows, rolling, e.upsPollInterval, time.Now())
	if out.hold {
		return nil
	}
	return e.handleRuleResult(ctx, rule, out.met, out.value, out.label, pgtype.UUID{}, pgtype.UUID{})
}

// upsRuleVerdict judges a rule on the UPSes it judges (upsRuleRows). A UPS a
// window covers drops out of a global rule, but where it alone would meet the
// condition the verdict holds, neither firing nor resolving, as every rule a
// window covers does: a window opened during an outage must not report the
// outage over.
func upsRuleVerdict(rule db.AlertRule, rows []db.ListUpsDevicesRow, windows []db.MaintenanceWindow, rolling []uuid.UUID,
	pollInterval time.Duration, now time.Time) upsRuleOutcome {
	judged, held := upsRuleRows(rule, rows, windows, rolling)
	if held {
		return upsRuleOutcome{hold: true}
	}
	out := upsRuleValue(rule.Metric, rule.Operator, rule.Threshold, upsAlertReadings(judged, pollInterval, now))
	if !out.hold && !out.met && rule.ScopeType != "ups" && len(judged) < len(rows) &&
		upsRuleValue(rule.Metric, rule.Operator, rule.Threshold, upsAlertReadings(rows, pollInterval, now)).met {
		return upsRuleOutcome{hold: true}
	}
	return out
}

// upsRuleRows are the UPSes a rule judges, in a new slice: for a global rule
// every one no maintenance window covers; for a rule of scope ups the one it
// names — none once that UPS is gone, so there is nothing to judge and a
// firing alert resolves. ups_unreachable leaves out the rolling ones, whose NUT
// server's node a rolling update is upgrading, rebooting or health-checking:
// down for the update, not lost. held is a rule of scope ups whose UPS a
// window covers, or, for ups_unreachable, a rolling one: its verdict holds,
// as a node's rules do under a window.
func upsRuleRows(rule db.AlertRule, rows []db.ListUpsDevicesRow, windows []db.MaintenanceWindow,
	rolling []uuid.UUID) (judged []db.ListUpsDevicesRow, held bool) {
	if rule.ScopeType == "ups" && rule.UpsID.Valid {
		own := uuid.UUID(rule.UpsID.Bytes)
		if upsInMaintenance(own, windows) || rule.Metric == MetricUPSUnreachable && slices.Contains(rolling, own) {
			return nil, true
		}
	}
	judged = make([]db.ListUpsDevicesRow, 0, len(rows))
	for _, r := range rows {
		if rule.ScopeType == "ups" && (!rule.UpsID.Valid || r.ID != uuid.UUID(rule.UpsID.Bytes)) ||
			rule.ScopeType != "ups" && upsInMaintenance(r.ID, windows) ||
			rule.Metric == MetricUPSUnreachable && slices.Contains(rolling, r.ID) {
			continue
		}
		judged = append(judged, r)
	}
	return judged, false
}

// upsInMaintenance reports whether one of windows covers the UPS.
func upsInMaintenance(upsID uuid.UUID, windows []db.MaintenanceWindow) bool {
	return slices.ContainsFunc(windows, func(w db.MaintenanceWindow) bool {
		return w.UpsID.Valid && uuid.UUID(w.UpsID.Bytes) == upsID
	})
}

// upsRuleOutcome is a UPS rule's verdict, its value, and the label naming the
// UPS behind it.
type upsRuleOutcome struct {
	value float64
	label string
	// judged is false when no UPS reports the value: nothing to judge, so the
	// condition is false and a firing alert resolves.
	judged bool
	met    bool
	// hold means no verdict at all, neither firing nor resolving: a UPS Nexara
	// cannot read was last seen meeting the condition (see upsRuleValue).
	hold bool
}

// upsAlertReading is one UPS as the rules see it.
type upsAlertReading struct {
	name                  string
	current               bool
	onBattery, replace    bool
	charge, runtime, load *float64 // runtime in seconds
}

// upsAlertReadings converts the device rows. A UPS on a server whose polling
// is turned off is left out: that is the operator's choice, not an outage.
func upsAlertReadings(rows []db.ListUpsDevicesRow, pollInterval time.Duration, now time.Time) []upsAlertReading {
	out := make([]upsAlertReading, 0, len(rows))
	for _, r := range rows {
		if !r.ServerEnabled {
			continue
		}
		var vars map[string]string
		_ = json.Unmarshal(r.Vars, &vars)
		num := func(key string) *float64 {
			if f, ok := nut.Number(vars, key); ok {
				return &f
			}
			return nil
		}
		var readAt time.Time
		if r.LastReadingAt.Valid {
			readAt = r.LastReadingAt.Time
		}
		status := nut.ParseStatus(r.Status)
		out = append(out, upsAlertReading{
			// upsd's own ups@host form, with the server's name for the host.
			name:    r.UpsName + "@" + r.ServerName,
			current: nut.ReadingCurrent(r.ServerEnabled, r.Reachability, readAt, pollInterval, now),
			// The OB flag rather than the low_battery state, which FSD on
			// mains also lands in.
			onBattery: status.OnBattery(),
			replace:   status.Has("RB"),
			charge:    num("battery.charge"),
			runtime:   num("battery.runtime"),
			load:      num("ups.load"),
		})
	}
	return out
}

// upsRuleValue judges a rule on the UPSes Nexara can read. One it cannot read
// matters only if it could tip the rule: the verdict holds while its last
// reading meets the condition (the UPS that was on battery went dark), and is
// otherwise given without it, so a dead ups.conf entry cannot silence a rule
// or keep its alert firing. A condition the readable UPSes already meet stands
// whatever the others hold, where hiding a UPS can only understate the value
// (a count or a highest against > or >=, a lowest against < or <=).
func upsRuleValue(metric, operator string, threshold float64, ups []upsAlertReading) upsRuleOutcome {
	if metric == MetricUPSUnreachable {
		names := upsNames(ups, func(u upsAlertReading) bool { return !u.current })
		out := upsCount(names, len(ups), "cannot be read", "cannot be read", "Every UPS can be read")
		out.met = compareValue(out.value, operator, threshold)
		return out
	}
	var current []upsAlertReading
	for _, u := range ups {
		if u.current {
			current = append(current, u)
		}
	}
	out := upsAggregate(metric, current, len(ups))
	out.met = out.judged && compareValue(out.value, operator, threshold)
	unread := len(ups) - len(current)
	if unread == 0 {
		return out
	}
	if !out.met || !upsSubsetBounds(metric, operator) {
		if all := upsAggregate(metric, ups, len(ups)); all.judged && compareValue(all.value, operator, threshold) {
			return upsRuleOutcome{hold: true}
		}
		out.met = false
	}
	out.label += fmt.Sprintf(" · %d UPS not read", unread)
	return out
}

// upsSubsetBounds says whether a condition some UPSes meet is met by all of
// them, whatever the rest read.
func upsSubsetBounds(metric, operator string) bool {
	switch metric {
	case MetricUPSBatteryCharge, MetricUPSRuntime:
		return operator == "<" || operator == "<="
	}
	return exceedsThreshold(operator)
}

// upsAggregate is a metric's value over ups, out of total watched.
func upsAggregate(metric string, ups []upsAlertReading, total int) upsRuleOutcome {
	switch metric {
	case MetricUPSOnBattery:
		names := upsNames(ups, func(u upsAlertReading) bool { return u.onBattery })
		out := upsRuleOutcome{value: float64(len(names)), judged: true,
			label: fmt.Sprintf("No UPS is on battery (%d watched)", total)}
		// The one with the least runtime left leads the label.
		var worst *upsAlertReading
		for i := range ups {
			if ups[i].onBattery && (worst == nil || lessOrNil(ups[i].runtime, worst.runtime)) {
				worst = &ups[i]
			}
		}
		if worst != nil {
			out.label = worst.name + " is on battery"
			if left := nut.Remaining(worst.charge, worst.runtime); left != "" {
				out.label += " · " + left
			}
			if len(names) > 1 {
				out.label += fmt.Sprintf(" (%d of %d UPS on battery)", len(names), total)
			}
		}
		return out
	case MetricUPSReplaceBattery:
		names := upsNames(ups, func(u upsAlertReading) bool { return u.replace })
		return upsCount(names, total, "reports its battery needs replacing",
			"report a battery that needs replacing", "No UPS reports a battery to replace")
	case MetricUPSBatteryCharge:
		w, n := upsExtreme(ups, func(u upsAlertReading) *float64 { return u.charge }, false)
		if w == nil {
			return upsRuleOutcome{label: "No UPS reports its battery charge"}
		}
		return upsRuleOutcome{value: *w.charge, judged: true,
			label: fmt.Sprintf("%s · %s%% charge (lowest of %d)", w.name, strconv.FormatFloat(*w.charge, 'f', 0, 64), n)}
	case MetricUPSRuntime:
		w, n := upsExtreme(ups, func(u upsAlertReading) *float64 { return u.runtime }, false)
		if w == nil {
			return upsRuleOutcome{label: "No UPS reports its runtime"}
		}
		// The threshold is in minutes; a UPS reports seconds.
		return upsRuleOutcome{value: *w.runtime / 60, judged: true,
			label: fmt.Sprintf("%s · %s runtime (shortest of %d)", w.name, nut.FormatRuntime(*w.runtime), n)}
	case MetricUPSLoad:
		w, n := upsExtreme(ups, func(u upsAlertReading) *float64 { return u.load }, true)
		if w == nil {
			return upsRuleOutcome{label: "No UPS reports its load"}
		}
		return upsRuleOutcome{value: *w.load, judged: true,
			label: fmt.Sprintf("%s · %s%% load (highest of %d)", w.name, strconv.FormatFloat(*w.load, 'f', 0, 64), n)}
	}
	return upsRuleOutcome{}
}

func upsNames(ups []upsAlertReading, match func(upsAlertReading) bool) []string {
	var names []string
	for _, u := range ups {
		if match(u) {
			names = append(names, u.name)
		}
	}
	return names
}

// upsCount is a count rule's outcome, its label naming up to three UPSes.
func upsCount(names []string, total int, one, several, none string) upsRuleOutcome {
	out := upsRuleOutcome{value: float64(len(names)), judged: true}
	shown := names[:min(len(names), 3)]
	switch {
	case len(names) == 0:
		out.label = fmt.Sprintf("%s (%d watched)", none, total)
	case len(names) == 1:
		out.label = names[0] + " " + one
	default:
		out.label = fmt.Sprintf("%d of %d UPS %s: %s", len(names), total, several, strings.Join(shown, ", "))
		if len(names) > len(shown) {
			out.label += fmt.Sprintf(" and %d more", len(names)-len(shown))
		}
	}
	return out
}

// upsExtreme is the UPS with the lowest (or highest) reading among those
// reporting one, and how many do.
func upsExtreme(ups []upsAlertReading, read func(upsAlertReading) *float64, highest bool) (worst *upsAlertReading, reporting int) {
	for i := range ups {
		v := read(ups[i])
		if v == nil {
			continue
		}
		reporting++
		if worst == nil || (highest && *v > *read(*worst)) || (!highest && *v < *read(*worst)) {
			worst = &ups[i]
		}
	}
	return worst, reporting
}

// lessOrNil orders a missing reading last.
func lessOrNil(a, b *float64) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	}
	return *a < *b
}
