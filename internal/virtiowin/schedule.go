package virtiowin

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// DefaultInterval is how often a cluster with no explicit schedule is checked.
// Upstream cuts a release every few months, so this is already far more often
// than it can change; the point of the tick is to close the gap between a
// release appearing and a cluster holding it, not to catch it within minutes.
const DefaultInterval = 6 * time.Hour

// cronParser matches the one in internal/scheduler: five fields, no seconds.
// Duplicated rather than shared because internal/scheduler imports this
// package, so the dependency cannot run the other way.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ValidateSchedule checks a cron expression and an IANA zone name.
//
// Both are optional and both are validated the same way: empty is the default
// (six-hourly, server time), anything else has to parse. A schedule that does
// not parse must be rejected at the API rather than stored, because the
// scheduler's fallback for an unparseable expression is to check anyway — so a
// typo would silently mean "every minute" rather than "never".
func ValidateSchedule(schedule, timezone string) error {
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return fmt.Errorf("unknown timezone %q: %w", timezone, err)
		}
	}
	if schedule == "" {
		return nil
	}
	spec, err := cronParser.Parse(schedule)
	if err != nil {
		return fmt.Errorf("invalid check schedule %q: %w", schedule, err)
	}
	// Parsing is not enough. The parser range-checks each field on its own, so
	// a date that never occurs — "0 3 31 4 *" (April 31), Feb 30, Sep 31 —
	// parses cleanly and then never matches. robfig gives up searching after
	// five years and answers with the ZERO time, which as a next_check_at is
	// permanently in the past: the cluster would be due on every 60s tick,
	// forever, and recomputing it each pass would never heal it.
	if next := spec.Next(time.Now()); next.IsZero() {
		return fmt.Errorf("check schedule %q never comes round — check the day-of-month against the month", schedule)
	}
	return nil
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
	spec, err := cronParser.Parse(schedule)
	if err != nil {
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
	next := spec.Next(from.In(loc))
	// A schedule that never comes round answers with the zero time, which the
	// API refuses on write. Clamp anyway: a row written by a build that
	// predates that check, or edited by hand, would otherwise be due on every
	// tick forever, and this function recomputing the same zero time each pass
	// is exactly what would stop it healing.
	if !next.After(from) {
		return from.Add(DefaultInterval)
	}
	return next
}
