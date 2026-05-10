// Package safeconv provides bounds-clamping numeric conversions used at
// the boundary between Go's native int and the int32 columns/parameters
// many sqlc-generated query types expect (Postgres column counts, page
// sizes, paginated limits/offsets, etc.).
//
// Clamping (rather than panicking) is the right shape for the call
// sites in this codebase: pagination limits and counts cannot
// meaningfully overflow in practice, and a 64-bit-to-32-bit conversion
// failing closed at the SQL layer would be a worse user experience
// than silently capping at math.MaxInt32. Five copies of this exact
// function previously lived in `internal/scanner/cve.go`,
// `internal/notifications/alert_engine.go`,
// `internal/api/handlers/safe_convert.go`, `internal/collector/sync.go`,
// and `internal/drs/executor.go`; they were consolidated here in
// Phase 5.2.
package safeconv

import "math"

// Int32 converts an int to int32, clamping to math.MaxInt32 / MinInt32
// on overflow. Satisfies gosec G115/G118.
func Int32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v) //nolint:gosec // bounds checked above
}
