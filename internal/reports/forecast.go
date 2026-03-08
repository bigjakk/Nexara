package reports

import (
	"math"
	"time"
)

// LinearRegression computes slope and intercept using least-squares.
// xs and ys must have equal length and at least 2 points.
// Returns slope, intercept, and whether the result is valid.
func LinearRegression(xs, ys []float64) (slope, intercept float64, ok bool) {
	n := len(xs)
	if n < 2 || n != len(ys) {
		return 0, 0, false
	}

	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		sumX += xs[i]
		sumY += ys[i]
		sumXY += xs[i] * ys[i]
		sumX2 += xs[i] * xs[i]
	}

	nf := float64(n)
	denom := nf*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-12 {
		return 0, 0, false
	}

	slope = (nf*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / nf
	return slope, intercept, true
}

// ForecastMetric projects when a metric will reach a threshold (e.g. 100% for CPU/memory).
// points are (time, value) pairs. threshold is the exhaustion level.
// Returns days until exhaustion and the projected date, or nil if no exhaustion projected.
func ForecastMetric(times []time.Time, values []float64, threshold float64) (daysToExhaust *float64, exhaustionDate *time.Time) {
	if len(times) < 2 || len(times) != len(values) {
		return nil, nil
	}

	// Convert times to days from start.
	start := times[0]
	xs := make([]float64, len(times))
	for i, t := range times {
		xs[i] = t.Sub(start).Hours() / 24.0
	}

	slope, intercept, ok := LinearRegression(xs, values)
	if !ok || slope <= 0 {
		// No growth trend or invalid — no exhaustion projected.
		return nil, nil
	}

	// Days from start when value reaches threshold.
	daysFromStart := (threshold - intercept) / slope
	now := times[len(times)-1]
	daysFromNow := daysFromStart - now.Sub(start).Hours()/24.0

	if daysFromNow <= 0 {
		// Already past threshold.
		return nil, nil
	}

	d := math.Round(daysFromNow*10) / 10
	t := now.Add(time.Duration(daysFromNow*24) * time.Hour)
	return &d, &t
}
