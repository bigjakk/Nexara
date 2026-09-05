package virtiowin

import (
	"testing"
	"time"
)

func TestValidateSchedule(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		timezone string
		wantErr  bool
	}{
		{name: "both empty is the default", schedule: "", timezone: ""},
		{name: "daily at 03:00", schedule: "0 3 * * *"},
		{name: "zone without a schedule is allowed", timezone: "America/Chicago"},
		{name: "schedule with a zone", schedule: "0 3 * * *", timezone: "Europe/Berlin"},
		{name: "unknown zone", schedule: "0 3 * * *", timezone: "Mars/Olympus", wantErr: true},

		// The cron shapes are cronspec's table to enumerate, not this one's —
		// these two rows are here to prove the delegation happens at all, one
		// per branch: a malformed expression, and one that parses but names a
		// date that never occurs (April has no 31st).
		{name: "prose is not a cron", schedule: "every day at 3", wantErr: true},
		{name: "April 31 never comes round", schedule: "0 3 31 4 *", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSchedule(tt.schedule, tt.timezone)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSchedule(%q, %q) error = %v, wantErr %v",
					tt.schedule, tt.timezone, err, tt.wantErr)
			}
		})
	}
}

// TestNextCheckInterval covers the empty-schedule case, which is an interval
// counted from the last check rather than a cron expression — and which every
// row upgraded in place from before 000100 carries.
func TestNextCheckInterval(t *testing.T) {
	from := time.Date(2026, 9, 1, 14, 32, 0, 0, time.UTC)
	got := NextCheck("", "", from)
	if want := from.Add(DefaultInterval); !got.Equal(want) {
		t.Errorf("NextCheck with no schedule = %v, want %v", got, want)
	}
}

// TestNextCheckHonoursTimezone is the whole reason check_timezone exists: a
// container runs on UTC, so "03:00" without a zone is the middle of the working
// day for much of the world. 03:00 in Chicago is 08:00 or 09:00 UTC depending
// on DST, so the assertion is on the wall clock in the zone, not on a fixed
// UTC offset.
func TestNextCheckHonoursTimezone(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	from := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC) // 15:00 in Chicago
	next := NextCheck("0 3 * * *", "America/Chicago", from)

	inChicago := next.In(chicago)
	if h, m := inChicago.Hour(), inChicago.Minute(); h != 3 || m != 0 {
		t.Errorf("next check = %v (%02d:%02d in Chicago), want 03:00 local", next, h, m)
	}
	if !next.After(from) {
		t.Errorf("next check %v is not after %v", next, from)
	}
	// The point of the zone: the same expression read as server time would
	// have fired seven hours earlier.
	if utc := NextCheck("0 3 * * *", "UTC", from); utc.Equal(next) {
		t.Errorf("America/Chicago and UTC produced the same next check (%v); the zone was ignored", next)
	}
}

// TestNextCheckFallsBackToInterval pins the fail-safe direction. A row written
// by an older build or edited by hand must not mean "never check again".
func TestNextCheckFallsBackToInterval(t *testing.T) {
	from := time.Date(2026, 9, 1, 14, 32, 0, 0, time.UTC)
	for _, tt := range []struct{ name, schedule, timezone string }{
		{"unparseable schedule", "not a cron", ""},
		{"unknown zone", "0 3 * * *", "Mars/Olympus"},
		// Rejected on write, so this can only be a row from an older build or
		// one edited by hand — the clamp is what stops it wedging the tick.
		{"unsatisfiable date", "0 3 31 4 *", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := NextCheck(tt.schedule, tt.timezone, from)
			if !got.After(from) {
				t.Fatalf("NextCheck(%q, %q) = %v, which is not in the future",
					tt.schedule, tt.timezone, got)
			}
			if got.IsZero() {
				t.Fatalf("NextCheck(%q, %q) returned the zero time; stored, that is due-forever",
					tt.schedule, tt.timezone)
			}
			// The unknown zone still has a usable schedule, so it lands on the
			// next 03:00 rather than on the interval; the unparseable and the
			// unsatisfiable ones degrade all the way.
			if tt.timezone == "" && !got.Equal(from.Add(DefaultInterval)) {
				t.Errorf("NextCheck(%q) = %v, want the %v interval",
					tt.schedule, got, DefaultInterval)
			}
		})
	}
}
