// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

// floorDiv64 divides rounding toward negative infinity.
func floorDiv64(a, b int64) int64 {
	if (a^b) < 0 && a%b != 0 {
		return a/b - 1
	}
	return a / b
}

// The DDA positions of the side edges and the connecting lines carry
// DDAFraction fraction bits, and a position's integer part is its
// dot: the nearest dot to the exact position, since the position
// starts half a dot past its start point. A step's error, the
// fraction the division drops, is below 2^-DDAFraction, so over the
// steps of a line in the drawing coordinate range, at any render
// scale, the accumulated error stays under the distance from any
// exact position to the nearest half dot, and only a position
// exactly half way between two dots takes its dot from the direction
// of the error: a step truncated toward zero leaves such a position
// at the dot toward the start point, and a step rounded away from
// zero at the dot away from it.
const DDAFraction = 32

// ddaHalf is half a dot in the DDA's fraction bits.
const ddaHalf = int64(1) << (DDAFraction - 1)

// ddaStart is the DDA position of start point p whose step is step:
// the point half a dot on, one unit short of that when a step toward
// + is truncated and when a step toward - is rounded away from zero,
// so that a half way position takes its dot in the direction the
// step's error leans (DDAFraction).
func ddaStart(p int32, step int64, away bool) int64 {
	bias := ddaHalf
	if (step > 0) == !away {
		bias--
	}
	return int64(p)<<DDAFraction + bias
}

// ddaStep is the DDA step covering delta in steps steps, truncated
// toward zero, or rounded away from zero when away is set. A zero
// step count is a point.
func ddaStep(delta int32, steps int32, away bool) int64 {
	if steps <= 0 {
		return 0
	}
	n := int64(steps)
	d := int64(delta) << DDAFraction
	if !away {
		return d / n
	}
	if d < 0 {
		return -((-d + n - 1) / n)
	}
	return (d + n - 1) / n
}

// DDAEdge is a walk from one point to another over an exact number
// of steps: the start position and the delta per step, at
// DDAFraction fraction bits. The polygon and distorted sprite setup
// places its side edges with it (EdgeAt, RightEdgeAt).
type DDAEdge struct {
	X, Y   int64 // start position
	DX, DY int64 // delta per step
}

// NewDDAEdge starts at (x1, y1) and reaches (x2, y2) after steps
// steps, a position half way between two dots taking the dot toward
// the start point: the walk of the A-D edge.
func NewDDAEdge(x1, y1, x2, y2, steps int32) DDAEdge {
	var e DDAEdge
	e.DX = ddaStep(x2-x1, steps, false)
	e.DY = ddaStep(y2-y1, steps, false)
	e.X = ddaStart(x1, e.DX, false)
	e.Y = ddaStart(y1, e.DY, false)
	return e
}

// NewRightDDAEdge starts at (x1, y1) and reaches (x2, y2) after
// steps steps, a position half way between two dots taking the dot
// away from the start point: the walk of the B-C edge.
func NewRightDDAEdge(x1, y1, x2, y2, steps int32) DDAEdge {
	var e DDAEdge
	e.DX = ddaStep(x2-x1, steps, true)
	e.DY = ddaStep(y2-y1, steps, true)
	e.X = ddaStart(x1, e.DX, true)
	e.Y = ddaStart(y1, e.DY, true)
	return e
}
