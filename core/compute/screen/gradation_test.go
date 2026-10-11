// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// gradationRow fills n pixels at rowOff of buf with a fixed pseudo
// random pattern: every fifth pixel transparent, the others opaque
// with priority bits set.
func gradationRow(buf compute.Layer, rowOff uint32, n int32) {
	seed := uint32(12345)
	for x := range n {
		seed = seed*1103515245 + 12345
		px := uint32(0)
		if x%5 != 3 {
			px = 3<<24 | PackColor(seed>>8&0xFF, seed>>16&0xFF, seed>>24)
		}
		buf[rowOff+uint32(x)] = px
	}
}

// TestGradationSegments holds the blur to the same result under any
// segmentation of the row into spans, at every step: the carries hold
// the pre-blur pixels left of each span, one-dot spans included.
func TestGradationSegments(t *testing.T) {
	const width = 320
	for _, step := range []int32{1, 2, 4, 8} {
		w := width * step
		p := GradationParams{Y: 1, Width: w, Step: step}
		rowOff := uint32(w)
		want := make(compute.Layer, 8192)
		gradationRow(want, rowOff, w)
		GradationSpan(want, &p, &GradationState{}, 0, w)
		for _, seg := range []int32{1, 2, 3, 7, 16, 17} {
			got := make(compute.Layer, 8192)
			gradationRow(got, rowOff, w)
			var st GradationState
			for x0 := int32(0); x0 < w; x0 += seg {
				GradationSpan(got, &p, &st, x0, min(x0+seg, w))
			}
			for x := range w {
				if got[rowOff+uint32(x)] != want[rowOff+uint32(x)] {
					t.Fatalf("step %d spans of %d: pixel %d = %#08x, want %#08x", step, seg, x, got[rowOff+uint32(x)], want[rowOff+uint32(x)])
				}
			}
		}
	}
}

// TestGradationBlocks holds the blur at step N on a row whose native
// pixels are expanded to N-dot blocks to the native blur expanded the
// same way: each sub-pixel reads the same sub-pixel of the blocks one
// and two native pixels to its left.
func TestGradationBlocks(t *testing.T) {
	const width = 320
	native := make(compute.Layer, 4096)
	gradationRow(native, 0, width)
	src := make(compute.Layer, 4096)
	copy(src, native)
	GradationSpan(native, &GradationParams{Width: width, Step: 1}, &GradationState{}, 0, width)
	for _, step := range []int32{2, 4, 8} {
		scaled := make(compute.Layer, 4096)
		for x := range int32(width) * step {
			scaled[x] = src[x/step]
		}
		GradationSpan(scaled, &GradationParams{Width: width * step, Step: step}, &GradationState{}, 0, width*step)
		for x := range int32(width) * step {
			if scaled[x] != native[x/step] {
				t.Fatalf("step %d: pixel %d = %#08x, want native pixel %d %#08x", step, x, scaled[x], x/step, native[x/step])
			}
		}
	}
}
