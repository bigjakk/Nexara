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
	// robfig answers an unsatisfiable expression with the zero time, which is
	// never after from — so this covers that case and every other answer that
	// would make a row due the moment it is written.
	if !next.After(from) {
		return time.Time{}, fmt.Errorf("%w: %q", ErrUnsatisfiableSchedule, schedule)
	}
	return next, nil
}

// NextValidRun is NextRunTime with an operator-facing message on failure. The
// message replaces the error rather than wrapping it, so ErrUnsatisfiableSchedule
// does NOT survive this call — callers that branch on the sentinel want
// NextRunTime, which still wraps it.
//
// This is the gate every write path goes through, so it is what keeps a new
// bad schedule out of the database; the guards in the scheduler are the safety
// net for rows written before this existed, or edited by hand. Callers that
// need the value as well as the verdict take it from here rather than
// validating and then parsing again — on scheduled_tasks the two must agree,
// because a next_run_at the row cannot use reads as "due now".
func NextValidRun(schedule string, from time.Time) (time.Time, error) {
	next, err := NextRunTime(schedule, from)
	switch {
	case err == nil:
		return next, nil
	case errors.Is(err, ErrUnsatisfiableSchedule):
		return time.Time{}, fmt.Errorf("cron expression %q never comes round — check the day of month against the month "+
			"(April, June, September and November have no 31st; February has no 30th)", schedule)
	default:
		// Returned as-is: NextRunTime already names the expression and carries
		// robfig's own field text, so wrapping it again put the expression in
		// a single 400 three times over.
		return time.Time{}, err
	}
}

// ValidateCron checks that a cron expression is well-formed AND that it can
// actually fire. Parsing alone is not validation — see ErrUnsatisfiableSchedule.
func ValidateCron(schedule string) error {
	_, err := NextValidRun(schedule, time.Now())
	return err
}
