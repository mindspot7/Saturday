// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

// TestSegmentAtMatchesStepping pins the closed form of a Bresenham
// segment's dot position and error term against stepping, over
// segments of every octant, slope class, and length, including
// points and axis-aligned segments.
func TestSegmentAtMatchesStepping(t *testing.T) {
	ends := [][4]int32{
		{0, 0, 100, 37}, {0, 0, 37, 100}, {10, 20, -90, 41}, {10, 20, -41, -90}, {5, 5, 5, 5},
		{0, 0, 50, 0}, {0, 0, 0, 50}, {-300, 200, 250, -180}, {7, -3, 8, 500}, {0, 0, -1, -1},
		{0, 0, 3, 2}, {0, 0, 2, 3}, {0, 0, 1000, 999}, {3, 3, -1000, 1001},
	}
	for _, e := range ends {
		var st Segment
		BeginSegment(&st, e[0], e[1], e[2], e[3], 0, 0)
		x, y, err := st.X1, st.Y1, -st.DX
		if !st.XMajor {
			err = -st.DY
		}
		for i := int32(0); i <= st.IMax; i++ {
			cx, cy, cerr := SegmentAt(&st, i)
			if cx != x || cy != y || cerr != err {
				t.Fatalf("segment %v dot %d: closed form (%d,%d) err %d, stepped (%d,%d) err %d", e, i, cx, cy, cerr, x, y, err)
			}
			if st.XMajor {
				x += st.SX
				err += 2 * st.DY
				if err >= 0 {
					y += st.SY
					err -= 2 * st.DX
				}
			} else {
				y += st.SY
				err += 2 * st.DX
				if err >= 0 {
					x += st.SX
					err -= 2 * st.DY
				}
			}
		}
		if x != e[2]+st.SX && y != e[3]+st.SY {
			t.Fatalf("segment %v: stepping ended at (%d,%d)", e, x, y)
		}
	}
}
