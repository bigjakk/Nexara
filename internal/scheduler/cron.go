package scheduler

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser is a standard cron parser with seconds field omitted.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// NextRunTime computes the next run time after `from` for the given cron expression.
func NextRunTime(schedule string, from time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", schedule, err)
	}
	return sched.Next(from), nil
}

// ValidateCron checks if a cron expression is valid.
func ValidateCron(schedule string) error {
	_, err := cronParser.Parse(schedule)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", schedule, err)
	}
	return nil
}
