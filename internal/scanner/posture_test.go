package scanner

import (
	"math"
	"testing"
)

// TestComputePostureScoreAnchors locks down the calibration anchors
// documented on ComputePostureScore. Tweaking the constants in cve.go
// will fail this test; that's intentional — the anchors are the contract.
func TestComputePostureScoreAnchors(t *testing.T) {
	cases := []struct {
		name                                          string
		critical, high, medium, low, unknown          int32
		minScore, maxScore                            float32
	}{
		{"clean cluster", 0, 0, 0, 0, 0, 100, 100},
		{"one low", 0, 0, 0, 1, 0, 97, 99},
		{"one critical", 1, 0, 0, 0, 0, 73, 76},
		{"hundred lows", 0, 0, 0, 100, 0, 85, 88},
		{"hundred unknowns", 0, 0, 0, 0, 100, 85, 88},
		{"thousand lows do not floor", 0, 0, 0, 1000, 0, 78, 82},
		{"crjlab actual", 0, 0, 3, 96, 18, 65, 72},
		{"five criticals", 5, 0, 0, 0, 0, 32, 38},
		// Quantity flooding doesn't beat severity:
		{"100 lows still scores higher than 1 critical",
			0, 0, 0, 100, 0, 85, 88},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ComputePostureScore(c.critical, c.high, c.medium, c.low, c.unknown)
			if got < c.minScore || got > c.maxScore {
				t.Errorf("score=%.2f, want in [%.2f,%.2f]", got, c.minScore, c.maxScore)
			}
		})
	}
}

func TestComputePostureScoreClamps(t *testing.T) {
	// Massive deduction should clamp to 0, not go negative.
	score := ComputePostureScore(100, 0, 0, 0, 0)
	if score != 0 {
		t.Errorf("expected clamp to 0, got %.2f", score)
	}
	// Empty input is exactly 100.
	if got := ComputePostureScore(0, 0, 0, 0, 0); got != 100 {
		t.Errorf("expected 100 for empty, got %.2f", got)
	}
}

func TestComputePostureScoreSeverityOrdering(t *testing.T) {
	// One CVE in a higher bucket must always deduct strictly more
	// than one CVE in a lower bucket.
	scores := []float32{
		ComputePostureScore(0, 0, 0, 0, 0), // clean
		ComputePostureScore(0, 0, 0, 1, 0), // 1 low
		ComputePostureScore(0, 0, 1, 0, 0), // 1 medium
		ComputePostureScore(0, 1, 0, 0, 0), // 1 high
		ComputePostureScore(1, 0, 0, 0, 0), // 1 critical
	}
	for i := 1; i < len(scores); i++ {
		if scores[i] >= scores[i-1] {
			t.Errorf("severity %d (score=%.2f) should deduct more than severity %d (score=%.2f)",
				i, scores[i], i-1, scores[i-1])
		}
	}
}

// Property check: a single critical CVE always deducts more than a
// large pile of lows. Without this, attackers could "bury" criticals.
func TestCriticalsBeatVolumeOfLows(t *testing.T) {
	critScore := ComputePostureScore(1, 0, 0, 0, 0)
	for _, n := range []int32{10, 100, 1000, 10000} {
		lowScore := ComputePostureScore(0, 0, 0, n, 0)
		if lowScore < critScore {
			t.Errorf("with %d lows, score=%.2f went below 1 critical's score=%.2f — quantity beat severity",
				n, lowScore, critScore)
		}
	}
}

func TestBucketDeductionMonotonic(t *testing.T) {
	// More CVEs in the same bucket must never decrease deduction.
	for w := float32(1); w <= 30; w += 5 {
		prev := float32(math.Inf(-1))
		for n := int32(0); n < 1000; n += 10 {
			got := bucketDeduction(w, n, 0)
			if got < prev {
				t.Errorf("non-monotonic at weight=%.1f n=%d: %.4f < %.4f", w, n, got, prev)
			}
			prev = got
		}
	}
}

func TestBucketDeductionCap(t *testing.T) {
	// Cap should clamp once the natural deduction exceeds it.
	cases := []struct {
		weight float32
		count  int32
		cap    float32
		want   float32
	}{
		{2, 1, 20, 2}, // 2 * log2(2) = 2 < cap
		{2, 1_000_000, 20, 20},
		{12, 1000, 0, 119.59}, // uncapped
	}
	for _, c := range cases {
		got := bucketDeduction(c.weight, c.count, c.cap)
		if math.Abs(float64(got-c.want)) > 0.5 {
			t.Errorf("bucketDeduction(%.0f, %d, %.0f) = %.2f, want ~%.2f",
				c.weight, c.count, c.cap, got, c.want)
		}
	}
}
