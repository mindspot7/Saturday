// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// GradationParams describes the row the gradation blur runs over: the
// row of the layer buffer, its width, and the distance in dots to the
// blur's left neighbors (1 native, N at scale N, so a sub-pixel reads
// the same sub-pixel of the blocks one and two native pixels to its
// left). The buffer is the host's binding.
type GradationParams struct {
	Y     int32
	Width int32
	Step  int32
}

// GradationCarries is the number of pre-blur pixels a span carries
// to the next: two neighbors at the largest step, 8.
const GradationCarries = 16

// GradationState carries the blur's carries across a row's spans:
// Carry[i] is the pre-blur value of the pixel i+1 left of the span,
// the first 2*Step of them used. It starts zero at the row's left
// edge, so the pixels left of the row read as transparent, and the
// spans of a row are consumed left to right.
type GradationState struct {
	Carry [GradationCarries]uint32
}

// GradationSpan blurs pixels [x0, x1) of the row in place: pixel x
// becomes (x-2*Step + x-Step + 2x) / 4 per channel from the pre-blur
// values of its left neighbors, keeping its priority and color
// calculation bits, and transparent pixels stay transparent.
// Processing right to left within the span keeps the in-span
// neighbors original.
func GradationSpan(buf compute.Layer, p *GradationParams, st *GradationState, x0, x1 int32) {
	rowOff := uint32(p.Y * p.Width)
	step := p.Step
	// The carries for the next span, taken before the span is blurred:
	// the pixels left of x1, from this span or this span's carries.
	var next [GradationCarries]uint32
	for i := range 2 * step {
		x := x1 - 1 - i
		if x >= x0 {
			next[i&(GradationCarries-1)] = buf[rowOff+uint32(x)]
		} else {
			next[i&(GradationCarries-1)] = st.Carry[(x0-1-x)&(GradationCarries-1)]
		}
	}
	for x := x1 - 1; x >= x0; x-- {
		cur := buf[rowOff+uint32(x)]
		if cur == 0 {
			continue
		}
		curR := int32(ColorR(cur))
		curG := int32(ColorG(cur))
		curB := int32(ColorB(cur))
		pri := cur &^ ColorMask

		var l1, l2 uint32
		if xl1 := x - step; xl1 >= x0 {
			l1 = buf[rowOff+uint32(xl1)]
		} else {
			l1 = st.Carry[(x0-1-xl1)&(GradationCarries-1)]
		}
		if xl2 := x - 2*step; xl2 >= x0 {
			l2 = buf[rowOff+uint32(xl2)]
		} else {
			l2 = st.Carry[(x0-1-xl2)&(GradationCarries-1)]
		}

		var l1r, l1g, l1b, l2r, l2g, l2b int32
		if l1 != 0 {
			l1r = int32(ColorR(l1))
			l1g = int32(ColorG(l1))
			l1b = int32(ColorB(l1))
		}
		if l2 != 0 {
			l2r = int32(ColorR(l2))
			l2g = int32(ColorG(l2))
			l2b = int32(ColorB(l2))
		}

		newR := (l2r + l1r + curR*2) / 4
		newG := (l2g + l1g + curG*2) / 4
		newB := (l2b + l1b + curB*2) / 4

		buf[rowOff+uint32(x)] = pri | PackColor(uint32(newR), uint32(newG), uint32(newB))
	}
	st.Carry = next
}
