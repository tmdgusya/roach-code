package cli

import "testing"

// TestShortTokens pins the compact token formatter's magnitude boundaries:
// raw counts below 1K, "K" suffix from 1K up to (but not including) 1M, and a
// one-decimal "M" suffix at and above 1M. Expected values are computed from the
// integer-division (K) and float-division (M) branches in shortTokens directly.
func TestShortTokens(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"zero", 0, "0"},
		{"sub-thousand", 999, "999"},
		{"exact-thousand", 1000, "1K"},
		{"hundreds-of-thousands", 142000, "142K"},
		{"just-below-million", 999999, "999K"},
		{"exact-million", 1000000, "1.0M"},
		{"millions-with-fraction", 2500000, "2.5M"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortTokens(tc.in); got != tc.want {
				t.Fatalf("shortTokens(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFormatCost pins the magnitude-scaled spend formatter: totals under 1 keep
// four decimals (so fractions of a cent stay visible) and totals at/above 1
// round to two decimals. The 0.99999 case verifies %.4f rounding lands on
// "1.0000" while still taking the sub-unit branch, and 1.0 verifies the >=1
// branch flips to two decimals.
func TestFormatCost(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0.0, "0.0000"},
		{"sub-unit-four-decimals", 0.0123, "0.0123"},
		{"sub-unit-rounds-up", 0.99999, "1.0000"},
		{"unit-two-decimals", 1.0, "1.00"},
		{"above-unit-rounds", 12.345, "12.35"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatCost(tc.in); got != tc.want {
				t.Fatalf("formatCost(%g) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
