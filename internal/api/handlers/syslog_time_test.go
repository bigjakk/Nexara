package handlers

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestNormalizeSyslogTime_RendersInNodeLocalTime is the regression lock for the
// bug this endpoint kept reproducing: Proxmox hands since/until to journalctl,
// which reads a wall-clock string in the NODE's timezone. Rendering an absolute
// instant as UTC therefore points at the node's future whenever the node is
// behind UTC, and journalctl answers "-- No entries --" — indistinguishable
// from a node with no logs.
//
// Any node in a zone behind UTC hits this, so it is the ordinary case, not a
// corner one.
func TestNormalizeSyslogTime_RendersInNodeLocalTime(t *testing.T) {
	const nodeOffset = -7 * time.Hour

	got, err := normalizeSyslogTime("since", "1h", nodeOffset)
	if err != nil {
		t.Fatalf("normalizeSyslogTime: %v", err)
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", got, time.UTC)
	if err != nil {
		t.Fatalf("rendered %q is not a wall clock Proxmox accepts: %v", got, err)
	}
	if !pveWallClock.MatchString(got) {
		t.Errorf("rendered %q does not match the form Proxmox validates against", got)
	}

	// Read back in the node's zone, the value must be ~1h in the node's past.
	instant := parsed.Add(-nodeOffset)
	delta := time.Since(instant)
	if delta < 55*time.Minute || delta > 65*time.Minute {
		t.Errorf("`1h` on a UTC-7 node resolved to %v ago in real terms, want ~1h.\n"+
			"rendered=%q — a value in the node's future is what produces '-- No entries --'",
			delta.Round(time.Minute), got)
	}
}

func TestNormalizeSyslogTime_Accepts(t *testing.T) {
	// Every in-window fixture is computed, not hard-coded. A literal that sits
	// inside the 90-day lookback today falls outside it 90 days from now, and
	// the test starts failing for a reason that has nothing to do with the code
	// — which already happened once here, to the epoch row below.
	recentUnix := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	recentDay := time.Now().Add(-24 * time.Hour)
	recentDate := recentDay.Format("2006-01-02")
	recentDateTime := recentDay.Format("2006-01-02 15:04")
	recentDateSeconds := recentDay.Format("2006-01-02 15:04:05")

	// The first count that overflows time.Duration for each unit — five digits
	// for weeks, so well inside the regex's 7. The product wraps NEGATIVE, so a
	// cap applied to the product instead of the count reads it as "under the
	// limit" and renders a wall clock centuries in the future.
	overflowWeeks := strconv.Itoa(int(math.MaxInt64/int64(7*24*time.Hour)) + 1)

	tests := []struct {
		name, value string
		wantErr     bool
	}{
		{"proxmox date", recentDate, false},
		{"proxmox date-time", recentDateTime, false},
		{"proxmox date-time-seconds", recentDateSeconds, false},
		{"relative hours", "1h", false},
		{"relative signed", "-90m", false},
		{"relative with ago", "30m ago", false},
		{"unix seconds", recentUnix, false},

		{"garbage", "yesterday", true},
		{"sql-ish", "2026-08-25'; DROP", true},
		// Rejected by the digit bound before any arithmetic.
		{"absurd offset", "9999999999999d", true},
		// The one that matters: passes the digit bound, overflows the multiply.
		{"offset at the time.Duration overflow boundary", overflowWeeks + "w", true},
		// Milliseconds would render a five-digit year Proxmox rejects.
		{"millisecond timestamp", recentUnix + "000", true},
		{"beyond the lookback cap", "1970-01-02", true},
		{"epoch", "1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSyslogTime("since", tt.value, 0)
			if tt.wantErr {
				if err == nil {
					t.Errorf("normalizeSyslogTime(%q) = %q, want an error", tt.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSyslogTime(%q): %v", tt.value, err)
			}
			if !pveWallClock.MatchString(got) {
				t.Errorf("normalizeSyslogTime(%q) = %q, which Proxmox's pattern rejects", tt.value, got)
			}
		})
	}
}

// The default has to be safe with no node clock available at all, since it is
// used before any offset is resolved.
func TestDefaultSyslogSince_IsPastAtEveryRealOffset(t *testing.T) {
	got := defaultSyslogSince()
	day, err := time.ParseInLocation("2006-01-02", got, time.UTC)
	if err != nil {
		t.Fatalf("default %q is not a date: %v", got, err)
	}
	// Largest real UTC offsets are -12 and +14.
	for _, offset := range []time.Duration{-12 * time.Hour, 0, 14 * time.Hour} {
		if instant := day.Add(-offset); instant.After(time.Now()) {
			t.Errorf("default since %q is in the future for a node at offset %v", got, offset)
		}
	}
	if strings.Contains(got, ":") {
		t.Errorf("default %q should stay date-only — the date form is the one Proxmox has always accepted", got)
	}
}

// TestNormalizeSyslogTime_NeverRendersAFutureWallClock is the property the
// whole since/until path exists to hold: whatever a caller sends, the string
// handed to journalctl must name a moment in the node's past. A future one is
// answered with "-- No entries --", which reads as a node with no logs.
func TestNormalizeSyslogTime_NeverRendersAFutureWallClock(t *testing.T) {
	offsets := []time.Duration{-12 * time.Hour, -7 * time.Hour, 0, 5*time.Hour + 30*time.Minute, 14 * time.Hour}
	values := []string{
		"1h", "-90m", "30m ago", "7d", "12w",
		strconv.Itoa(int(math.MaxInt64/int64(7*24*time.Hour))+1) + "w",
		strconv.Itoa(int(math.MaxInt64/int64(24*time.Hour))+1) + "d",
		"9999999w", "9999999d", "9999999h",
		strconv.FormatInt(time.Now().Unix(), 10),
	}
	for _, offset := range offsets {
		for _, v := range values {
			got, err := normalizeSyslogTime("since", v, offset)
			if err != nil {
				continue // rejected outright, which is a fine answer
			}
			parsed, perr := time.ParseInLocation("2006-01-02 15:04:05", got, time.UTC)
			if perr != nil {
				t.Errorf("normalizeSyslogTime(%q, %v) = %q, not a wall clock: %v", v, offset, got, perr)
				continue
			}
			if instant := parsed.Add(-offset); instant.After(time.Now().Add(time.Minute)) {
				t.Errorf("normalizeSyslogTime(%q, offset=%v) = %q, which is %v in the node's FUTURE",
					v, offset, got, time.Until(instant).Round(time.Second))
			}
		}
	}
}

// TestParseJournalTime_NeverReturnsAFutureTimestamp is the same property for
// the journal endpoint, which has no wall-clock cap to fall back on.
func TestParseJournalTime_NeverReturnsAFutureTimestamp(t *testing.T) {
	for _, v := range []string{
		"1h", "7d", "12w", "9999999w",
		strconv.Itoa(int(math.MaxInt64/int64(7*24*time.Hour))+1) + "w",
	} {
		ts, err := parseJournalTime("since", v)
		if err != nil {
			continue
		}
		if time.Unix(ts, 0).After(time.Now().Add(time.Minute)) {
			t.Errorf("parseJournalTime(%q) = %d, which is in the future", v, ts)
		}
	}
}

// TestNeedsNodeClock_MatchesNormalization pins the gate to the thing it gates.
// If needsNodeClock says no but normalizeSyslogTime renders differently per
// offset, the offset is silently 0 and the timezone bug is back for that input.
func TestNeedsNodeClock_MatchesNormalization(t *testing.T) {
	recent := time.Now().Add(-2 * time.Hour)
	for _, v := range []string{
		"", recent.Format("2006-01-02"), recent.Format("2006-01-02 15:04:05"),
		"1h", "30m ago", strconv.FormatInt(recent.Unix(), 10),
		"yesterday", "not-a-time",
	} {
		utc, errUTC := normalizeSyslogTime("since", v, 0)
		shifted, errShift := normalizeSyslogTime("since", v, -7*time.Hour)
		// Only compare where both resolve; a rejected value needs no offset.
		if errUTC != nil || errShift != nil {
			if needsNodeClock(v) && errUTC == nil {
				t.Errorf("needsNodeClock(%q) = true but the value does not resolve per-offset", v)
			}
			continue
		}
		offsetMatters := utc != shifted
		if got := needsNodeClock(v); got != offsetMatters {
			t.Errorf("needsNodeClock(%q) = %v, but the rendering %s with the offset (%q vs %q)",
				v, got, map[bool]string{true: "changes", false: "does not change"}[offsetMatters], utc, shifted)
		}
	}
}
