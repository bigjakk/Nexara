package scheduler

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser is a standard cron parser with seconds field omitted.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ErrUnsatisfiableSchedule reports a cron expression that parses but names a
// moment that never arrives.
//
// The parser range-checks each field on its own — day-of-month 1-31, month
// 1-12 — so a combination that never occurs is accepted: "0 8 31 4 *" (April
// has 30 days), "0 8 30 2 *", "0 8 31 9 *". robfig then searches five years
// ahead for a match, gives up, and answers with the ZERO time.
//
// That zero time is the whole reason this error exists. Stored as a next-run
// column it is year 1, i.e. permanently in the past, so the row matches its
// due predicate on every tick — and recomputing it after each run produces the
// same zero time, so it never heals. For scheduled_tasks that means re-running
// the action (a snapshot, or a reboot) every 60 seconds indefinitely.
var ErrUnsatisfiableSchedule = errors.New("cron expression never comes round")

// NextRunTime computes the next run time after `from` for the given cron
// expression, and refuses to answer with a time that is not in the future.
//
// Callers must treat the error as "this schedule cannot fire" rather than as a
// transient failure, and must NOT fall back to writing a zero or null next-run
// value without checking what their own due predicate makes of it — the two
// consumers disagree: report_schedules requires `next_run_at <= now()`, so
// NULL is inert there, while scheduled_tasks matches `next_run_at IS NULL OR
// next_run_at <= now()`, where NULL means due now.
func NextRunTime(schedule string, from time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", schedule, err)
	}
	next := sched.Next(from)
	// IsZero is the case robfig actually produces; the After check is the
	// general form of the same property, so no caller can be handed a
	// timestamp that would make its row due the moment it is written.
	if next.IsZero() || !next.After(from) {
		return time.Time{}, fmt.Errorf("%w: %q", ErrUnsatisfiableSchedule, schedule)
	}
	return next, nil
}

// ValidateCron checks that a cron expression is well-formed AND that it can
// actually fire. Parsing alone is not validation — see ErrUnsatisfiableSchedule.
//
// This is the gate every write path goes through, so it is what keeps a new
// bad schedule out of the database; the guards in the scheduler are the safety
// net for rows written before this existed, or edited by hand.
func ValidateCron(schedule string) error {
	if _, err := NextRunTime(schedule, time.Now()); err != nil {
		if errors.Is(err, ErrUnsatisfiableSchedule) {
			return fmt.Errorf("cron expression %q never comes round — check the day of month against the month "+
				"(April, June, September and November have no 31st; February has no 30th)", schedule)
		}
		return fmt.Errorf("invalid cron expression %q: %w", schedule, err)
	}
	return nil
}
