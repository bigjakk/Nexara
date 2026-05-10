package safeconv

import (
	"math"
	"testing"
)

func TestInt32(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int32
	}{
		{"zero", 0, 0},
		{"small positive", 42, 42},
		{"small negative", -42, -42},
		{"max int32", math.MaxInt32, math.MaxInt32},
		{"min int32", math.MinInt32, math.MinInt32},
		{"overflow positive clamps to MaxInt32", math.MaxInt32 + 1, math.MaxInt32},
		{"overflow negative clamps to MinInt32", math.MinInt32 - 1, math.MinInt32},
		{"way over clamps to MaxInt32", math.MaxInt64, math.MaxInt32},
		{"way under clamps to MinInt32", math.MinInt64, math.MinInt32},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Int32(tc.in); got != tc.want {
				t.Errorf("Int32(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
