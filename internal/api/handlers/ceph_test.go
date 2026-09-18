package handlers

import (
	"slices"
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
	fallback, ok := cephMetricsTimeframes[CephMetricsDefaultTimeframe]
	if !ok {
		t.Fatalf("default timeframe %q is not in the table",
			CephMetricsDefaultTimeframe)
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

// TestCephMetricsTimeframesCoverTheTable holds the exported enum and the
// window table together.
//
// CephMetricsTimeframes is what the endpoint declaration in
// internal/api/registry_ceph.go validates ?timeframe= against, and
// cephMetricsTimeframes is what cephMetricsWindow looks the value up in.
// Two lists, one vocabulary: a timeframe added to the table but not the
// slice is unreachable through the API, and one added to the slice but not
// the table passes validation and then silently serves the default window.
// Both directions fail here.
func TestCephMetricsTimeframesCoverTheTable(t *testing.T) {
	for _, tf := range CephMetricsTimeframes {
		if _, ok := cephMetricsTimeframes[tf]; !ok {
			t.Errorf("CephMetricsTimeframes offers %q, but cephMetricsTimeframes has no window for it — "+
				"the schema would accept it and cephMetricsWindow would silently serve the default", tf)
		}
	}
	for tf := range cephMetricsTimeframes {
		if !slices.Contains(CephMetricsTimeframes, tf) {
			t.Errorf("cephMetricsTimeframes defines a window for %q, but CephMetricsTimeframes does not "+
				"offer it — the schema's enum makes it unreachable", tf)
		}
	}
	if !slices.Contains(CephMetricsTimeframes, CephMetricsDefaultTimeframe) {
		t.Errorf("the default timeframe %q is not in CephMetricsTimeframes, so the declaration's "+
			"Default would fail its own enum", CephMetricsDefaultTimeframe)
	}
}

// TestCephOSDActionsCoverTheDisruptiveSet keeps the exported action
// vocabulary honest against the two places that reason about an action.
//
// CephOSDActions is what the pre-flight endpoint validates ?action=
// against; cephDisruptiveOSDActions decides whether that action gets a
// redundancy warning, and osdServingAfter decides what the cluster looks
// like afterwards. An action the enum accepts but osdServingAfter has no
// case for falls to the default branch and is graded as if nothing
// happened — an advisory check that answers "ok" to an action it does not
// understand.
func TestCephOSDActionsCoverTheDisruptiveSet(t *testing.T) {
	for action := range cephDisruptiveOSDActions {
		if !slices.Contains(CephOSDActions, action) {
			t.Errorf("cephDisruptiveOSDActions names %q, which CephOSDActions does not offer — "+
				"the warning it carries is unreachable", action)
		}
	}

	// osdServingAfter has no exported shape to inspect, so this drives it:
	// every declared action must produce a verdict that depends on the
	// action rather than on the OSD's current state alone. A serving OSD
	// is the input, so anything disruptive must answer false and anything
	// else must answer true.
	serving := cephOSDResponse{ID: 1, Up: 1, In: 1, Host: "pve-01"}
	for _, action := range CephOSDActions {
		got := osdServingAfter(serving, action)
		if want := !cephDisruptiveOSDActions[action]; got != want {
			t.Errorf("osdServingAfter(serving, %q) = %v, want %v — the action is %sdisruptive",
				action, got, want, map[bool]string{true: "", false: "not "}[cephDisruptiveOSDActions[action]])
		}
	}
}
