package cronspec

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// cronTestFrom is a fixed instant so the leap-year cases below are deterministic.
var cronTestFrom = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// cronCases is the one fixture both entry points are checked against.
// ValidateCron is NextValidRun is NextRunTime, so a schedule's verdict is the
// same on both paths by construction — a second list is how the two drift
// apart, and every case added here is now exercised by both tests.
var cronCases = []struct {
	name     string
	schedule string
	wantErr  bool
	// wantMsg, when set, must appear in ValidateCron's error — the operator
	// sees this string in a 400, and "invalid cron" alone does not tell them
	// which half of "April 31" is the problem.
	wantMsg string
}{
	{name: "daily", schedule: "0 8 * * *"},
	{name: "weekly on Monday", schedule: "0 8 * * 1"},
	{name: "every six hours", schedule: "0 */6 * * *"},
	{name: "last day of a 31-day month", schedule: "0 8 31 1 *"},

	// Parseable but unsatisfiable: each field is in range on its own, so
	// the parser accepts it, and robfig then searches five years, gives
	// up, and returns the zero time. Stored, that is a next-run column
	// permanently in the past.
	{name: "April 31 does not exist", schedule: "0 8 31 4 *", wantErr: true, wantMsg: "never comes round"},
	{name: "February 30 does not exist", schedule: "0 8 30 2 *", wantErr: true, wantMsg: "never comes round"},
	{name: "September 31 does not exist", schedule: "0 8 31 9 *", wantErr: true, wantMsg: "never comes round"},
	{name: "June 31 does not exist", schedule: "0 8 31 6 *", wantErr: true, wantMsg: "never comes round"},
	{name: "November 31 does not exist", schedule: "0 8 31 11 *", wantErr: true, wantMsg: "never comes round"},

	// February 29 DOES come round — just not every year. Rejecting it
	// would be over-correction: it is a legitimate quadrennial schedule.
	{name: "February 29 is legal in a leap year", schedule: "0 8 29 2 *"},

	{name: "empty is not a schedule", schedule: "", wantErr: true},
	{name: "too few fields", schedule: "0 8 * *", wantErr: true},
	{name: "seconds field is not our parser", schedule: "0 0 8 * * *", wantErr: true},
	{name: "hour out of range", schedule: "0 99 * * *", wantErr: true},
	{name: "prose is not a cron", schedule: "every day at 8", wantErr: true},
}

func TestValidateCron(t *testing.T) {
	for _, tt := range cronCases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCron(tt.schedule)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateCron(%q) error = %v, wantErr %v", tt.schedule, err, tt.wantErr)
			}
			if tt.wantMsg != "" && err != nil && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ValidateCron(%q) error = %q, want it to mention %q", tt.schedule, err, tt.wantMsg)
			}
		})
	}
}

// TestNextRunTimeNeverAnswersInThePast is the property the whole fix rests on.
// Every caller writes the result into a next-run column whose due predicate is
// some form of `<= now()`, so a zero or past timestamp means "due on every
// tick, forever" — and because the recompute after each run produces the same
// value, it never heals.
func TestNextRunTimeNeverAnswersInThePast(t *testing.T) {
	for _, tt := range cronCases {
		t.Run(tt.name, func(t *testing.T) {
			next, err := NextRunTime(tt.schedule, cronTestFrom)
			// Same verdict as ValidateCron reached, from a fixed instant
			// rather than time.Now(). That holds for every case in the table
			// this side of 2096: robfig bounds its search at from.Year()+5, and
			// the next 29 Feb after 2096 is 2104 — 2100 is not a leap year. A
			// new case with a gap that wide would answer differently on the two
			// clocks, so check the bound before adding one.
			if (err != nil) != tt.wantErr {
				t.Fatalf("NextRunTime(%q) error = %v, wantErr %v", tt.schedule, err, tt.wantErr)
			}
			if err != nil {
				// The contract on the error path: no usable time, and the
				// zero value is what callers must NOT persist.
				if !next.IsZero() {
					t.Errorf("NextRunTime(%q) returned an error and a non-zero time %v", tt.schedule, next)
				}
				return
			}
			if next.IsZero() {
				t.Fatalf("NextRunTime(%q) = the zero time with no error", tt.schedule)
			}
			if !next.After(cronTestFrom) {
				t.Errorf("NextRunTime(%q) = %v, which is not after %v", tt.schedule, next, cronTestFrom)
			}
		})
	}
}

func TestNextRunTimeUnsatisfiableIsTyped(t *testing.T) {
	// Callers branch on this to tell "cannot ever fire" from a transient
	// failure, so it has to survive the wrapping.
	_, err := NextRunTime("0 8 31 4 *", cronTestFrom)
	if !errors.Is(err, ErrUnsatisfiableSchedule) {
		t.Fatalf("NextRunTime error = %v, want it to wrap ErrUnsatisfiableSchedule", err)
	}
	// A malformed expression is a different failure and must not be
	// misreported as an unsatisfiable one. The err == nil check is what keeps
	// that meaningful: errors.Is(nil, ...) is false, so without it a parser
	// that started accepting this would pass the assertion below.
	_, err = NextRunTime("not a cron", cronTestFrom)
	if err == nil {
		t.Fatal(`NextRunTime("not a cron") succeeded, want a parse error`)
	}
	if errors.Is(err, ErrUnsatisfiableSchedule) {
		t.Errorf("a parse failure was reported as unsatisfiable: %v", err)
	}
}

func TestNextRunTimeComputesTheExpectedInstant(t *testing.T) {
	tests := []struct {
		schedule string
		want     time.Time
	}{
		{"0 8 * * *", time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)},
		{"0 13 * * *", time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)},
		// 2026 is not a leap year and 2027 is not either, so the next 29 Feb
		// is in 2028 — the case that proves the guard does not simply reject
		// anything it cannot find within a year.
		{"0 8 29 2 *", time.Date(2028, 2, 29, 8, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.schedule, func(t *testing.T) {
			got, err := NextRunTime(tt.schedule, cronTestFrom)
			if err != nil {
				t.Fatalf("NextRunTime(%q): %v", tt.schedule, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("NextRunTime(%q) = %v, want %v", tt.schedule, got, tt.want)
			}
		})
	}
}
