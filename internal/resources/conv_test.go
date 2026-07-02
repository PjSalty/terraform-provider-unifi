package resources

import (
	"math"
	"testing"
)

// TestSafeInt32 proves the saturation guard: in-range values pass through
// unchanged, out-of-range values clamp to the int32 bounds instead of
// wrapping.
func TestSafeInt32(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want int32
	}{
		{"zero", 0, 0},
		{"vlan range value", 4009, 4009},
		{"negative in range", -42, -42},
		{"max int32 exact", math.MaxInt32, math.MaxInt32},
		{"min int32 exact", math.MinInt32, math.MinInt32},
		{"clamps above max", math.MaxInt32 + 1, math.MaxInt32},
		{"clamps below min", math.MinInt32 - 1, math.MinInt32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeInt32(tc.in); got != tc.want {
				t.Errorf("safeInt32(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
