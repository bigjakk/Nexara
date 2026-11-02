package nut

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Number reads the first of keys that holds a finite number. upsd sends every
// value as text, and a driver may send "unknown" or leave a variable out.
func Number(vars map[string]string, keys ...string) (float64, bool) {
	for _, k := range keys {
		v, ok := vars[k]
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

// StaleAfter is how old a stored reading may be before it no longer says what
// the UPS is doing: three poll intervals, and never under two minutes. Without
// it a poller that stopped (lost leadership, a crash) would leave the last
// reading standing as if it were current.
func StaleAfter(pollInterval time.Duration) time.Duration {
	return max(3*pollInterval, 2*time.Minute)
}

// ReadingCurrent says whether a stored reading describes the UPS now: its
// server is polled, the last poll read it (reachability "ok", as the poller
// records a read), and the reading is no older than StaleAfter. Anything else
// is Nexara not knowing, which is never shown as on mains or on battery.
func ReadingCurrent(serverEnabled bool, reachability string, readAt time.Time, pollInterval time.Duration, now time.Time) bool {
	return serverEnabled && reachability == "ok" && !readAt.IsZero() && now.Sub(readAt) <= StaleAfter(pollInterval)
}

// Remaining is what a battery has left, "52% · 9m 40s left", or "" when the
// UPS reports neither.
func Remaining(charge, runtime *float64) string {
	var parts []string
	if charge != nil {
		parts = append(parts, strconv.FormatFloat(*charge, 'f', 0, 64)+"%")
	}
	if runtime != nil {
		parts = append(parts, FormatRuntime(*runtime)+" left")
	}
	return strings.Join(parts, " · ")
}

// FormatRuntime renders seconds as "45s", "9m 40s" or "1h 05m".
func FormatRuntime(sec float64) string {
	s := int(math.Max(0, sec))
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh %02dm", s/3600, (s%3600)/60)
	case s >= 60:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
