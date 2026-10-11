// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

// TestOpLinesCoordinateLimitScales holds the vertex bound to the op's
// scale: the largest native coordinate the walk produces, 40959,
// draws at every scale shift when scaled by it, and a coordinate at
// the native limit or past it draws nothing at its own scale.
func TestOpLinesCoordinateLimitScales(t *testing.T) {
	const largest = 40959
	for shift := uint32(0); shift <= 5; shift++ {
		op := DrawOp{Kind: OpPolygon}
		op.Pixel.ScaleShift = shift
		for k := range op.X {
			op.X[k], op.Y[k] = largest<<shift, -largest<<shift
		}
		if got := OpLines(&op); got != 1 {
			t.Errorf("shift %d: coordinate %d has %d lines, want 1", shift, largest<<shift, got)
		}
		op.X[1] = nativeCoordLimit << shift
		if got := OpLines(&op); got != 0 {
			t.Errorf("shift %d: coordinate %d has %d lines, want 0", shift, nativeCoordLimit<<shift, got)
		}
		if shift > 0 {
			// The same scaled coordinate read at the scale below is
			// past that scale's bound.
			op.X[1] = largest << shift
			op.Pixel.ScaleShift = shift - 1
			if got := OpLines(&op); got != 0 {
				t.Errorf("shift %d read at %d: coordinate %d has %d lines, want 0", shift, shift-1, largest<<shift, got)
			}
		}
	}
}
