// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestCompositeFieldBootstrap checks the field bootstrap row mask: the
// composite, over the one run of a row with no windows, writes its
// row and, with the mask set, the row FBRow xor the mask, which is the
// other field's row at native (mask 1) and the same sub-row of the
// other field's block at scale (mask N), and no other row.
func TestCompositeFieldBootstrap(t *testing.T) {
	const width = 16
	const rows = 64
	back := PackColor(10, 20, 30)
	var runs WindowRuns
	BuildWindowRuns(LineWindow{}, LineWindow{}, width, &runs)
	cases := []struct {
		fbRow, mask int32
	}{
		{5, 0},
		{5, 1},
		{4, 1},
		{2*(2*3+1) + 1, 2},
		{4*(2*1+0) + 3, 4},
		{8*(2*2+1) + 6, 8},
	}
	for _, c := range cases {
		out := make(compute.Framebuffer, width*rows)
		p := CompositeParams{Width: width, FBRow: c.fbRow, FieldBootstrap: uint32(c.mask), BackRGB: back, Runs: runs}
		CompositeSpan(&CompositeInputs{}, out, &p, 0, width)
		for row := range int32(rows) {
			written := row == c.fbRow || (c.mask != 0 && row == c.fbRow^c.mask)
			for x := range int32(width) {
				got := out[row*width+x]
				want := uint32(0)
				if written {
					want = 0xFF000000 | back
				}
				if got != want {
					t.Fatalf("FBRow %d mask %d: row %d dot %d = %#08x, want %#08x", c.fbRow, c.mask, row, x, got, want)
				}
			}
		}
	}
}
