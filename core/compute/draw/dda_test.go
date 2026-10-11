// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

func TestFloorDiv64(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{10, 3, 3},   // positive: 10/3 = 3.33 -> 3
		{-10, 3, -4}, // negative dividend: -10/3 = -3.33 -> -4
		{10, -3, -4}, // negative divisor: 10/-3 = -3.33 -> -4
		{-10, -3, 3}, // both negative: -10/-3 = 3.33 -> 3
		{9, 3, 3},    // exact division
		{-9, 3, -3},  // exact negative
		{0, 5, 0},    // zero
		{7, 1, 7},    // divisor 1
		{-7, 1, -7},  // negative with divisor 1
	}
	for _, tc := range cases {
		got := floorDiv64(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("floorDiv64(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
