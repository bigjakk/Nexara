package handlers

import (
	"testing"
	"time"
)

// The bug this guards: GetHistorical used to hand the raw ceph_cluster_metrics
// hypertable straight to the client. At the 10s collection interval that
// docker-compose ships, a 7-day window is tens of thousands of rows — and
// CephMetricsChart draws that into four charts on a 60s refetch. The PBS equivalent produced a 6.4MB response that
// froze the whole SPA for ~10s. Bucketing is what keeps the response bounded,
// so the invariant worth pinning is the point count, not the constants.
//
// This ranges over the timeframe table rather than restating it, so a
// timeframe added later is held to the same bound without a new test row.
func TestCephMetricsWindowBounded(t *testing.T) {
	// A few hundred points is already finer than the pixels available to draw
	// them; well past that and we are shipping data no one can see.
	const maxPointsPerSeries = 256

	for timeframe := range cephMetricsTimeframes {
		t.Run(timeframe, func(t *testing.T) {
			window, bucketSeconds := cephMetricsWindow(timeframe)

			if bucketSeconds <= 0 {
				// time_bucket() rejects a zero or negative width outright, so
				// this would be a 500 on every request rather than a silent
				// fallback to raw rows.
				t.Fatalf("bucketSeconds = %d, must be positive", bucketSeconds)
			}
			if window <= 0 {
				t.Fatalf("window = %v, must be positive", window)
			}

			points := int64(window/time.Second) / int64(bucketSeconds)
			if points > maxPointsPerSeries {
				t.Errorf("timeframe %q yields %d points "+
					"(window %v / bucket %ds), want <= %d",
					timeframe, points, window, bucketSeconds,
					maxPointsPerSeries)
			}
		})
	}
}

// An unrecognised ?timeframe= must still resolve to a bounded window — the
// query is driven by whatever the client sends. The default doubles as the
// documented value for a missing parameter, so it has to be a real key.
func TestCephMetricsWindowFallback(t *testing.T) {
	fallback, ok := cephMetricsTimeframes[cephMetricsDefaultTimeframe]
	if !ok {
		t.Fatalf("default timeframe %q is not in the table",
			cephMetricsDefaultTimeframe)
	}

	for _, timeframe := range []string{"", "not-a-timeframe", "1H", "30d"} {
		t.Run("falls back: "+timeframe, func(t *testing.T) {
			window, bucketSeconds := cephMetricsWindow(timeframe)
			if window != fallback.window || bucketSeconds != fallback.bucketSeconds {
				t.Errorf("cephMetricsWindow(%q) = (%v, %d), want (%v, %d)",
					timeframe, window, bucketSeconds,
					fallback.window, fallback.bucketSeconds)
			}
		})
	}
}
