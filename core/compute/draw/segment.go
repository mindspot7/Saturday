// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// Segment is a Bresenham segment (a line command, one leg of a
// polyline, or the A-B segment of a polygon whose side edges are
// points): its start, its absolute deltas and step directions, and
// the next dot. A dot's position and error term follow from its
// index by closed form (SegmentAt), so a span call derives its state
// from I. Both endpoints are inclusive.
type Segment struct {
	X1, Y1 int32  // start
	DX, DY int32  // absolute deltas
	SX, SY int32  // step directions
	I      int32  // next dot
	IMax   int32  // last dot
	Key    uint32 // the segment's draw order key (LineKey)
	XMajor bool   // the major axis is X
	GStart uint32 // Gouraud values at the endpoints
	GEnd   uint32
}

// BeginSegment starts the segment from (x1, y1) to (x2, y2).
func BeginSegment(st *Segment, x1, y1, x2, y2 int32, gStart, gEnd uint32) {
	st.X1 = x1
	st.Y1 = y1
	st.DX = x2 - x1
	st.DY = y2 - y1
	st.SX = 1
	if st.DX < 0 {
		st.SX = -1
		st.DX = -st.DX
	}
	st.SY = 1
	if st.DY < 0 {
		st.SY = -1
		st.DY = -st.DY
	}
	st.XMajor = st.DX >= st.DY
	st.IMax = st.DY
	if st.XMajor {
		st.IMax = st.DX
	}
	st.I = 0
	st.GStart = gStart
	st.GEnd = gEnd
}

// SegmentAt is the dot position and error term at dot i of the
// segment: with the major axis advanced i dots, the minor axis has
// advanced floor((2*minor*i + major) / (2*major)) dots, which is
// exactly what i Bresenham steps from the error term -major reach.
func SegmentAt(st *Segment, i int32) (x, y, err int32) {
	major, minor := int64(st.DX), int64(st.DY)
	if !st.XMajor {
		major, minor = minor, major
	}
	m := int64(0)
	if major > 0 {
		m = (2*minor*int64(i) + major) / (2 * major)
	}
	err = int32(-major + 2*minor*int64(i) - 2*major*m)
	if st.XMajor {
		return st.X1 + st.SX*i, st.Y1 + st.SY*int32(m), err
	}
	return st.X1 + st.SX*int32(m), st.Y1 + st.SY*i, err
}

// SegmentSpan draws dots [st.I, i1) of the segment, where i1 is at
// most st.IMax+1, or to the first YieldChunk boundary at or past the
// cycle budget with the segment unfinished, with op's color and pixel
// write parameters, and returns the cycles charged, pixCycles per
// dot.
func SegmentSpan(fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *Segment, i1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	x, y, err := SegmentAt(st, st.I)
	for st.I < i1 {
		ic := st.I + YieldChunk
		if ic > i1 {
			ic = i1
		}
		for st.I < ic {
			gouraud := uint32(0)
			if op.Pixel.CC >= 4 {
				gouraud = LerpGouraud(st.GStart, st.GEnd, st.I, st.IMax+1)
			}
			PutDot(fb, rs, &op.Pixel, x, y, op.Color, gouraud, st.Key)

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
			st.I++
			cycles += pixCycles
		}
		if cycles >= budget && st.I < i1 {
			break
		}
	}
	return cycles
}

// SegmentTime charges dots [st.I, i1) of the segment without drawing
// them, as SegmentSpan would charge them, pixCycles per dot in the
// same YieldChunk pieces against the same budget.
func SegmentTime(st *Segment, i1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	for st.I < i1 {
		ic := st.I + YieldChunk
		if ic > i1 {
			ic = i1
		}
		cycles += (ic - st.I) * pixCycles
		st.I = ic
		if cycles >= budget && st.I < i1 {
			break
		}
	}
	return cycles
}

// PolylineLeg is the vertices of leg (0 to 3) of a polyline: A-B,
// B-C, C-D, D-A.
func PolylineLeg(op *DrawOp, leg int32) (x1, y1, x2, y2 int32, gStart, gEnd uint32) {
	next := (leg + 1) & 3
	return op.X[leg], op.Y[leg], op.X[next], op.Y[next], op.Gouraud[leg], op.Gouraud[next]
}
