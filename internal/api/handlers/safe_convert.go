package handlers

import "math"

// safeInt32 converts an int to int32, clamping to math.MaxInt32 if the value
// would overflow. This satisfies gosec G115.
func safeInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v) //nolint:gosec // bounds checked above
}
