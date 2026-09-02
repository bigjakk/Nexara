package virtiowin

import (
	"fmt"
	"time"

	"github.com/bigjakk/nexara/internal/cronspec"
)

// DefaultInterval is how often a cluster with no explicit schedule is checked.
// Upstream cuts a release every few months, so this is already far more often
// than it can change; the point of the tick is to close the gap between a
// release appearing and a cluster holding it, not to catch it within minutes.
const DefaultInterval = 6 * time.Hour

// ValidateSchedule checks a cron expression and an IANA zone name.
//
// Both are optional and both are validated the same way: empty is the default
// (six-hourly, server time), anything else has to parse. A schedule that does
// not parse must be rejected at the API rather than stored, because NextCheck's
// fallback for one is the six-hourly interval — so a typo would silently mean
// "the default" rather than the schedule the operator typed.
func ValidateSchedule(schedule, timezone string) error {
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return fmt.Errorf("unknown timezone %q: %w", timezone, err)
		}
	}
	if schedule == "" {
		return nil
	}
	// cronspec.ValidateCron is parse + "does it ever come round": the parser
	// range-checks each field on its own, so a date that never occurs —
	// "0 3 31 4 *" (April 31), Feb 30, Sep 31 — parses cleanly and then never
	// matches. Stored, robfig's zero-time answer is a next_check_at
	// permanently in the past: the cluster would be due on every 60s tick,
	// forever, and recomputing it each pass would never heal it.
	return cronspec.ValidateCron(schedule)
}

// NextCheck returns when a cluster should next be checked, given its schedule
// and the moment it was last checked.
//
// An empty schedule is an interval, not a cron expression: from + 6h. Counting
// from the last check rather than from process start is the point — a restart
// loop used to mean an upstream fetch per boot.
//
// An unparseable schedule or unknown zone falls back to the interval rather
// than to "never". The API validates on write, so reaching that branch means
// the row was written by an older build or edited by hand; checking too often
// is recoverable, silently never checking again is not.
func NextCheck(schedule, timezone string, from time.Time) time.Time {
	if schedule == "" {
		return from.Add(DefaultInterval)
	}
	loc := time.Local
	if timezone != "" {
		if l, err := time.LoadLocation(timezone); err == nil {
			loc = l
		}
	}
	// robfig's SpecSchedule advances in the location of the time it is given,
	// so the zone has to be applied to `from` — not to the result.
	//
	// One fallback for both failures: unparseable, and "parses but never comes
	// round" (which cronspec.NextRunTime reports rather than answering with
	// robfig's zero time). The API refuses both on write, so a row that
	// reaches either was written by an older build or edited by hand — and
	// without the clamp it would be due on every tick forever, since each
	// recompute yields the same unusable answer.
	next, err := cronspec.NextRunTime(schedule, from.In(loc))
	if err != nil {
		return from.Add(DefaultInterval)
	}
	return next
}
