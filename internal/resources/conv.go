package resources

import "math"

// safeInt32 narrows an int64 that schema validators have already
// range-checked. The saturation guard is defense-in-depth: it makes the
// conversion provably lossless to static analysis (gosec G115/G109) and
// keeps behavior sane if a validator is ever bypassed.
func safeInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}
