// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// At scale each dot of the scaled walk, the native algorithm on the
// vertices at N times, claims the N by N square of sub-dots on its +
// sides, as a native dot covers its block: the dot at (x, y) claims
// [x, x+N) by [y, y+N). A sub-dot claimed by several dots belongs to
// the one the walk reaches last, and is written when that dot draws:
// a dot the texel rules skip claims its square and leaves it empty,
// so a texture's transparent dots end the opaque ones at their
// blocks. Along a line the major coordinate moves one sub-dot per
// step and the minor coordinate moves one way, one sub-dot at most
// per step, and a hole fill dot lies between its step's dot and the
// one before, so in draw order the dots' minor coordinates are
// monotone, and what a dot keeps of its square is its square less
// the square of the dot drawn next: the dot's columns the next dot's
// square does not reach keep the whole slice, [q, q+N), and the
// others keep the slice up to the next dot's minor coordinate, or
// from the next dot's slice end when the minor coordinate falls,
// which the dots after the next never reach. The next dot's square
// reaches every column of the dot's but the one on the - side of the
// walk when it is the next step's own dot, one step on along the
// major axis, or the next step's hole fill dot at the next step's
// column; it reaches them all when it is the next step's hole fill
// dot at the dot's own column. A connecting line whose next line
// shares its major axis leaves the sub-dots the next line claims at
// a column to it: the next line is drawn after it, so those sub-dots
// belong to its dots, and a sub-dot inside a part is then written by
// the last line claiming it alone. The next line's claims at a
// column are the range between the minor coordinates at the ends of
// its steps whose squares reach the column, since they are monotone.
// A line whose dots all draw one value (a polygon or a segment
// without Gouraud shading) is drawn per column instead, each column's
// coverage by the entry's dots as one range. The positions are closed
// forms in 32-bit arithmetic, since the code also runs as a compute
// shader, where a private array or a loop carried across columns
// costs more than a multiply.

// squareGeom is the geometry of a line drawn at scale: its form, its
// major axis and the direction its steps advance along it, the major
// coordinate of step 0, its length, the last step it draws in all,
// Cut, whether its record is pending, and its minor coordinates: for
// a connecting line the minor coordinate at step 0 and at the last
// step and the position step's minor part, DDAFraction fraction
// bits, and for a segment the minor coordinate of dot 0, the
// direction it advances, and the segment's major and minor deltas.
// HoleOwn says where a step's hole fill dot lies: at the step's own
// major coordinate with the minor coordinate of the step before, or
// at the major coordinate of the step before with the step's own
// minor coordinate.
type squareGeom struct {
	Form    uint32
	AxisX   bool
	Holes   bool
	HoleOwn bool
	Pending bool
	Dir     int32
	P0      int32
	Len     int32
	Cut     int32
	Lq, Rq  int32
	Dq      int64
	Sq, Ss  int32
	SMajor  int32
	SMinor  int32
}

// squareLine is a line an entry draws at scale: its geometry, the
// steps the entry draws, [First, Last], and the connecting line or
// segment state its values come from. The steps after Last through
// Cut belong to a later entry of the op: their squares take the
// sub-dots they cover from the entry's steps, which the later entry
// draws, and are not written.
type squareLine struct {
	G     squareGeom
	First int32
	Last  int32
	Quad  QuadLine
	Seg   Segment
}

// quadTexel is the dot of the character at column srcU of the row at
// rowBase in op's color mode.
func quadTexel(vram *compute.VDP1VRAM, op *DrawOp, rowBase uint32, srcU int32) uint32 {
	switch op.ColorMode {
	case 0, 1:
		return RowDotNibble(vram, rowBase, srcU)
	case 2, 3, 4:
		return RowDotByte(vram, rowBase, srcU)
	case 5:
		return RowDotWord(vram, rowBase, srcU)
	}
	return 0
}

// minor is the minor coordinate of the dot at step k of g. A
// connecting line's is its position step's minor part times k over
// the start, the DDA's value, and its right end at the last step; a
// segment's is the minor advance SegmentAt gives. The products are
// taken at 64 bits, as the walk takes them: a line thousands of dots
// long from off screen, within the coordinates the walk admits, has
// its on-screen steps past the 32-bit range.
func (g *squareGeom) minor(k int32) int32 {
	if g.Form == OpPolyline || g.Form == OpLine {
		m := int32(0)
		if g.SMajor > 0 {
			m = int32((2*int64(g.SMinor)*int64(k) + int64(g.SMajor)) / (2 * int64(g.SMajor)))
		}
		return g.Sq + g.Ss*m
	}
	if k >= g.Len {
		return g.Rq
	}
	return int32((ddaStart(g.Lq, g.Dq, false) + g.Dq*int64(k)) >> DDAFraction)
}

// owner is the native step of the step blocks sb that the dot at
// step k of g belongs to (stepBlocks.step).
func (g *squareGeom) owner(sb *stepBlocks, shift uint32, k int32) int32 {
	m, q := g.P0+g.Dir*k, g.minor(k)
	if g.AxisX {
		return sb.step(m, q, shift)
	}
	return sb.step(q, m, shift)
}

// squareFirstStep is the first step in [0, last] of g belonging to
// native step v or a later one when inc is set, v or an earlier one
// when it is not, or last + 1 when none does: the native steps the
// steps belong to move one way along the line, in the direction inc
// names.
func squareFirstStep(g *squareGeom, sb *stepBlocks, shift uint32, last, v int32, inc bool) int32 {
	lo, hi := int32(0), last+1
	for lo < hi {
		mid := lo + (hi-lo)/2
		a := g.owner(sb, shift, mid)
		if inc && a >= v || !inc && a <= v {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// squareOwnerRises reports whether the native step a step of g
// belongs to rises with the step: the line's coordinate along the
// native line's major axis, its own major coordinate when the two
// share the axis and its minor coordinate otherwise, advances in the
// native line's direction. A coordinate that holds still gives every
// step one native step, which either order finds.
func squareOwnerRises(g *squareGeom, sb *stepBlocks) bool {
	if g.AxisX == sb.AxisX {
		return g.Dir == sb.Dir
	}
	d := g.Dq
	if g.Form == OpPolyline || g.Form == OpLine {
		d = int64(g.Ss)
	}
	return d == 0 || (d > 0) == (sb.Dir > 0)
}

// squareStepsOwned is the steps [lo, hi] of g, of last + 1 steps,
// belonging to native steps [js, je]; lo above hi when none do.
func squareStepsOwned(g *squareGeom, sb *stepBlocks, shift uint32, last, js, je int32) (lo, hi int32) {
	if js > je {
		return 1, 0
	}
	if squareOwnerRises(g, sb) {
		return squareFirstStep(g, sb, shift, last, js, true), squareFirstStep(g, sb, shift, last, je+1, true) - 1
	}
	return squareFirstStep(g, sb, shift, last, je, false), squareFirstStep(g, sb, shift, last, js-1, false) - 1
}

// squareStepsEnd is the last step of g, of last + 1 steps, belonging
// to native steps 0 through je, -1 when none does.
func squareStepsEnd(g *squareGeom, sb *stepBlocks, shift uint32, last, je int32) int32 {
	if je < 0 {
		return -1
	}
	if squareOwnerRises(g, sb) {
		return squareFirstStep(g, sb, shift, last, je+1, true) - 1
	}
	return squareFirstStep(g, sb, shift, last, -1, false) - 1
}

// squareQuadGeom sets g's geometry from connecting line st. The hole
// fill dot lies at (px, prevPy) when the line advances x and y in the
// same direction (HoleFillDot), which is the step's own major
// coordinate on an X-major line and the step before's on a Y-major
// one.
func squareQuadGeom(g *squareGeom, st *QuadLine, form uint32) {
	lineX, backward := walkDirection(st.RX-st.LX, st.RY-st.LY)
	g.Form = form
	g.AxisX = lineX
	g.Holes = true
	g.HoleOwn = lineX == ((st.RX > st.LX) == (st.RY > st.LY))
	g.Dir = 1
	if backward {
		g.Dir = -1
	}
	g.Len = st.Len
	g.P0, g.Lq, g.Rq, g.Dq = st.LY, st.LX, st.RX, st.DPX
	if lineX {
		g.P0, g.Lq, g.Rq, g.Dq = st.LX, st.LY, st.RY, st.DPY
	}
}

// squareSegmentGeom sets g's geometry from segment st.
func squareSegmentGeom(g *squareGeom, st *Segment, form uint32) {
	g.Form = form
	g.Holes = false
	g.HoleOwn = false
	g.AxisX = st.XMajor
	g.Len = st.IMax
	g.Dir, g.P0, g.Sq, g.Ss, g.SMajor, g.SMinor = st.SY, st.Y1, st.X1, st.SX, st.DY, st.DX
	if st.XMajor {
		g.Dir, g.P0, g.Sq, g.Ss, g.SMajor, g.SMinor = st.SX, st.X1, st.Y1, st.SY, st.DX, st.DY
	}
}

// nativeStepBlocks is the step blocks of native connecting line nl of
// native and the line's length.
func nativeStepBlocks(native *DrawOp, nl int32) (sb stepBlocks, length int32) {
	var nst QuadLine
	quadLineGeometry(native, &nst, nl)
	return quadStepBlocks(&nst), nst.Len
}

// quadLineBounds is where native line nl of the op, of nlen steps,
// ends for an entry bounded by end and for the line as a whole: the
// entry's last step is LineEnd's step before when end is set, else
// the line's record when it has one, else its length, and the line's
// cut is at its record or its length. An entry's last line bounded by
// LineEnd may be one the walk had not ended, whose record is pending:
// the steps past the entry's are then unknown, and the line's cut is
// the entry's end, so the squares of the steps the entry draws are
// not cut by steps the end code rule may never reach. The entry drawn
// next then writes the sub-dots at the boundary those squares reach,
// at most N - 1 past it, a second time.
func quadLineBounds(b *Buffers, op *DrawOp, nl, nlen, end int32) (je, cut int32, pending bool) {
	je, cut = nlen, nlen
	if RecordStride(op) != 0 {
		rec := LoadLineEnd(b.Records, op.RecordBase, nl)
		pending = rec < 0
		if !pending {
			cut = min(rec, cut)
			je = cut
		}
	}
	if end >= 0 {
		je = min(end-1, nlen)
	}
	if pending {
		cut = je
	}
	return je, cut, pending
}

// squareQuadLine sets l up as connecting line i of scaled op, in the
// block of native line nl of native: the entry's steps are the
// sub-steps of that line's steps from start through the entry's end
// (quadLineBounds), and the line's cut the sub-steps through the
// line's.
func squareQuadLine(b *Buffers, l *squareLine, op, native *DrawOp, form uint32, i, nl, start, end int32) {
	sb, nlen := nativeStepBlocks(native, nl)
	shift := op.Pixel.ScaleShift
	je, cut, pending := quadLineBounds(b, op, nl, nlen, end)
	BeginQuadLine(op, &l.Quad, i)
	squareQuadGeom(&l.G, &l.Quad, form)
	l.G.Pending = pending
	l.First, l.Last = squareStepsOwned(&l.G, &sb, shift, l.G.Len, start, je)
	squareClip(l, op)
	l.G.Cut = l.Last
	if cut != je {
		l.G.Cut = max(squareStepsEnd(&l.G, &sb, shift, l.G.Len, cut), l.Last)
	}
}

// squareNextQuad sets g up as the geometry of connecting line i of
// scaled op, the line after one drawn, in the block of native line nl
// of native, bounded by end as the entry bounds it: its cut alone is
// found, from the line's record or the entry's end when the record
// is pending.
func squareNextQuad(b *Buffers, g *squareGeom, op, native *DrawOp, form uint32, i, nl, end int32) {
	sb, nlen := nativeStepBlocks(native, nl)
	_, cut, pending := quadLineBounds(b, op, nl, nlen, end)
	var st QuadLine
	quadLineGeometry(op, &st, i)
	squareQuadGeom(g, &st, form)
	g.Pending = pending
	g.Cut = squareStepsEnd(g, &sb, op.Pixel.ScaleShift, g.Len, cut)
}

// squareSegment sets l up as leg line of scaled op (a polyline's leg,
// or a line's or a point-sided polygon's one segment) with native leg
// of native: its steps are the sub-steps in the blocks of the native
// leg's dots from start through its end, LineEnd's dot before when
// end is set.
func squareSegment(l *squareLine, op, native *DrawOp, form uint32, line, start, end int32) {
	x1, y1, x2, y2, gStart, gEnd := op.X[0], op.Y[0], op.X[1], op.Y[1], op.Gouraud[0], op.Gouraud[1]
	nx1, ny1, nx2, ny2 := native.X[0], native.Y[0], native.X[1], native.Y[1]
	if op.Kind == OpPolyline {
		x1, y1, x2, y2, gStart, gEnd = PolylineLeg(op, line)
		nx1, ny1, nx2, ny2, _, _ = PolylineLeg(native, line)
	}
	var nst Segment
	BeginSegment(&nst, nx1, ny1, nx2, ny2, 0, 0)
	je := nst.IMax
	if end >= 0 {
		je = min(end-1, je)
	}
	sb := segmentStepBlocks(&nst)
	BeginSegment(&l.Seg, x1, y1, x2, y2, gStart, gEnd)
	squareSegmentGeom(&l.G, &l.Seg, form)
	l.G.Pending = false
	shift := op.Pixel.ScaleShift
	l.First, l.Last = squareStepsOwned(&l.G, &sb, shift, l.G.Len, start, je)
	squareClip(l, op)
	l.G.Cut = l.Last
	if je != nst.IMax {
		l.G.Cut = max(squareStepsEnd(&l.G, &sb, shift, l.G.Len, nst.IMax), l.Last)
	}
}

// squareMinorSteps is the steps [lo, hi] of g, of last + 1 steps,
// whose minor coordinate lies in [v0, v1]; lo above hi when none
// does. The minor coordinate is monotone along the line, rising when
// its step is positive, so each end is a bisection.
func squareMinorSteps(g *squareGeom, last, v0, v1 int32) (lo, hi int32) {
	d := g.Dq
	if g.Form == OpPolyline || g.Form == OpLine {
		d = int64(g.Ss)
	}
	rising := d >= 0
	// The first step whose minor coordinate is at least v0 when
	// rising, at most v1 when falling: the first inside.
	a, b := int32(0), last+1
	for a < b {
		mid := a + (b-a)/2
		q := g.minor(mid)
		if rising && q >= v0 || !rising && q <= v1 {
			b = mid
		} else {
			a = mid + 1
		}
	}
	lo = a
	// The first step past the range.
	a, b = lo, last+1
	for a < b {
		mid := a + (b-a)/2
		q := g.minor(mid)
		if rising && q > v1 || !rising && q < v0 {
			b = mid
		} else {
			a = mid + 1
		}
	}
	return lo, a - 1
}

// squareClip cuts the steps the entry draws of l to the steps whose
// squares can land inside the system clip, with a step of slack at
// each end for the hole fill dot a step draws at the position before
// its own: the steps whose major coordinate, one per step, and whose
// minor coordinate lie within the drawing area widened by N - 1 on
// its low sides, the reach of a square. A line whose squares all lie
// outside draws no step. The walk skips the same dots, whose writes
// the clip discards, and at scale a part off the screen by a margin
// holds millions of them.
func squareClip(l *squareLine, op *DrawOp) {
	g := l.G
	m := int32(1)<<op.Pixel.ScaleShift - 1
	clipMajor, clipMinor := op.Pixel.ClipY, op.Pixel.ClipX
	if g.AxisX {
		clipMajor, clipMinor = op.Pixel.ClipX, op.Pixel.ClipY
	}
	lo, hi := -m-g.P0, clipMajor-g.P0
	if g.Dir < 0 {
		lo, hi = g.P0-clipMajor, g.P0+m
	}
	qlo, qhi := squareMinorSteps(&g, g.Len, -m, clipMinor)
	lo, hi = max(lo, qlo)-1, min(hi, qhi)+1
	l.First, l.Last = max(l.First, lo), min(l.Last, hi)
	if l.First > l.Last {
		l.First, l.Last = 1, 0
	}
}

// squareSteps is the steps of g whose dots lie at columns p-N+1
// through p, [a, z-1], with z the step after, whose hole fill dot can
// lie at the column before its own; neither is clamped to the line's
// steps.
func squareSteps(g *squareGeom, p, n int32) (a, z int32) {
	if g.Dir > 0 {
		return p - n + 1 - g.P0, p - g.P0 + 1
	}
	return g.P0 - p, g.P0 - p + n
}

// squareClaims is the range of column p the squares of the dots of
// steps [s0, s1] of g claim, hole fill dots included, lo above hi
// when none does. The minor coordinates are monotone along the line,
// so the range runs between the minor coordinates at the ends of the
// steps whose dots reach the column and N - 1 past the greater: the
// steps whose own dots lie at the column, and, for the hole fill
// dots, the step before the first when a hole fill dot shares its
// step's column, since it lies at the minor coordinate of the step
// before, or the step after the last when it lies at the column
// before its step's, since it lies at its step's own.
func squareClaims(g *squareGeom, p, n, s0, s1 int32) (lo, hi int32) {
	a, z := squareSteps(g, p, n)
	k0, k1 := max(a, s0), min(z-1, s1)
	if g.Holes {
		if g.HoleOwn {
			if k0 <= k1 && k0 >= 1 {
				k0--
			}
		} else {
			h0, h1 := max(a+1, s0, 1), min(z, s1)
			if k0 > k1 {
				k0, k1 = h0, h1
			} else if h0 <= h1 {
				k1 = max(k1, h1)
			}
		}
	}
	if k0 > k1 {
		return 1, 0
	}
	q0, q1 := g.minor(k0), g.minor(k1)
	return min(q0, q1), max(q0, q1) + n - 1
}

// squareCut is the range the next line c claims at column p when cut
// is set, lo above hi otherwise.
func squareCut(cut bool, c *squareGeom, p, n int32) (lo, hi int32) {
	if !cut {
		return 1, 0
	}
	return squareClaims(c, p, n, 0, c.Cut)
}

// putSquareCut writes sub-dots [lo, hi] of column p less [cutLo,
// cutHi], the range the next line claims, with the value: a plain
// replace under the system clip (SimpleWrite) stores them directly,
// the decision PutDot would reach.
func putSquareCut(fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, simple bool, key uint32, axisX bool, p, lo, hi, cutLo, cutHi int32, pixel, gouraud uint32) {
	for q := lo; q <= hi; q++ {
		if q >= cutLo && q <= cutHi {
			q = cutHi
			continue
		}
		x, y := q, p
		if axisX {
			x, y = p, q
		}
		if simple {
			if x >= 0 && x <= op.Pixel.ClipX && y >= 0 && y <= op.Pixel.ClipY {
				fb.PutU16BE(uint32(y*op.Pixel.FBWidth+x)*2, uint16(pixel))
			}
		} else {
			PutDot(fb, rs, &op.Pixel, x, y, pixel, gouraud, key)
		}
	}
}

// squareBetween is the range a dot at minor coordinate q keeps of a
// column the next dot's square reaches when the next dot lies at
// minor coordinate nq: its slice up to the next dot's, or from the
// next dot's slice end when the minor coordinate falls; empty when
// the two coincide.
func squareBetween(q, nq, n int32) (lo, hi int32) {
	if nq > q {
		return q, min(q+n-1, nq-1)
	}
	return max(q, nq+n), q + n - 1
}

// squareStepChunk draws the steps of chunk j0 of line l, a textured
// or shaded line, each step its hole fill dot's range and its own
// dot's: a dot's square less the next dot's, where the next dot is
// the next step's hole fill dot when the next step draws one, else
// the next step's dot, and the whole square at the line's last step.
// The step's columns run from its own on the + side; the column the
// next step's dot does not reach is the step's own for a line walking
// + and the last for one walking -. The texture position and the
// Gouraud value advance by their steps, as the walk's spans advance
// them. When cut is set, c is a line drawn after l sharing its major
// axis, and l leaves the sub-dots c claims to it.
func squareStepChunk(b *Buffers, op *DrawOp, l *squareLine, key uint32, j0 int32, cut bool, c *squareGeom) {
	n := int32(1) << op.Pixel.ScaleShift
	shift := op.Pixel.ScaleShift
	simple := SimpleWrite(&op.Pixel)
	g := l.G
	ja, jz := max(j0, l.First), min(j0+GenChunk-1, l.Last)
	if ja > jz {
		return
	}
	textured := op.Kind == OpDistortedSprite
	shaded := op.Pixel.CC >= 4
	segment := g.Form == OpPolyline || g.Form == OpLine
	// A segment's Gouraud value is interpolated over its length and it
	// has no texture, so its chunk sets up neither stepper.
	var u int32
	var gs GouraudStepper
	if textured {
		u = quadU(&l.Quad, ja, shift)
	}
	if shaded && !segment {
		gs = l.Quad.Gouraud
		gs.Advance(ja)
	}
	qp, qj := int32(0), g.minor(ja)
	if ja >= 1 {
		qp = g.minor(ja - 1)
	}
	for j := ja; j <= jz; j++ {
		next := j+1 <= g.Cut
		qn := qj
		if next {
			qn = g.minor(j + 1)
		}
		drawn, pixel, gouraud := true, op.Color, uint32(0)
		if textured {
			srcU := QuadSrcU(op, &l.Quad, u>>(16+shift))
			dot := quadTexel(b.VRAM, op, l.Quad.RowBase, srcU)
			var ec EndCodeCounter
			drawn = TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, l.Quad.CheckECD, &ec, j)
			if drawn {
				pixel = DotToPixel(b.VRAM, dot, op.Color, op.ColorMode)
			}
			u += l.Quad.DU
		}
		if shaded {
			if segment {
				gouraud = LerpGouraud(l.Seg.GStart, l.Seg.GEnd, j, l.Seg.IMax+1)
			} else {
				gouraud = gs.Value(shift)
				gs.Step()
			}
		}
		if drawn {
			cj := g.P0 + g.Dir*j
			free := cj
			if g.Dir < 0 {
				free = cj + n - 1
			}
			if g.Holes && j >= 1 && qp != qj {
				if g.HoleOwn {
					lo, hi := squareBetween(qp, qj, n)
					for col := cj; col < cj+n; col++ {
						cutLo, cutHi := squareCut(cut, c, col, n)
						putSquareCut(b.FB, b.Round, op, simple, key, g.AxisX, col, lo, hi, cutLo, cutHi, pixel, gouraud)
					}
				} else {
					col := free - g.Dir
					cutLo, cutHi := squareCut(cut, c, col, n)
					putSquareCut(b.FB, b.Round, op, simple, key, g.AxisX, col, qj, qj+n-1, cutLo, cutHi, pixel, gouraud)
				}
			}
			lo, hi := qj, qj+n-1
			whole := !next
			if next && g.Holes && !g.HoleOwn && qn != qj {
				lo, hi = squareBetween(qj, qn, n)
				whole = true
			}
			if whole {
				for col := cj; col < cj+n; col++ {
					cutLo, cutHi := squareCut(cut, c, col, n)
					putSquareCut(b.FB, b.Round, op, simple, key, g.AxisX, col, lo, hi, cutLo, cutHi, pixel, gouraud)
				}
			} else {
				cutLo, cutHi := squareCut(cut, c, free, n)
				putSquareCut(b.FB, b.Round, op, simple, key, g.AxisX, free, lo, hi, cutLo, cutHi, pixel, gouraud)
				if !g.Holes && qn != qj {
					// A segment's next dot is the next step's, one
					// column on, whose square reaches every column
					// but the free one: those keep the slice up to
					// its minor coordinate.
					lo, hi = squareBetween(qj, qn, n)
					for col := cj; col < cj+n; col++ {
						if col == free {
							continue
						}
						cutLo, cutHi = squareCut(cut, c, col, n)
						putSquareCut(b.FB, b.Round, op, simple, key, g.AxisX, col, lo, hi, cutLo, cutHi, pixel, gouraud)
					}
				}
			}
		}
		qp, qj = qj, qn
	}
}

// squareColumnOf is the column at ordinal u of g: the columns run
// from the line's step 0 column on the - side, u from First - 1 (0
// when the entry starts at step 0) to Last + N - 1, the first the
// column before the entry's first dot, where that step's hole fill
// dot can lie, and the last N - 1 the columns past the entry's last
// dot on the + side.
func squareColumnOf(g *squareGeom, u, n int32) int32 {
	if g.Dir < 0 {
		return g.P0 - u + n - 1
	}
	return g.P0 + u
}

// squareFlatChunk draws chunk j0 of line l, a line whose dots all draw
// the op's color, per column: each column's coverage by the entry's
// steps as one range, less the range the steps after the entry's
// claim, which lies at one end of it since the minor coordinates are
// monotone, and less the range c claims when cut is set.
func squareFlatChunk(b *Buffers, op *DrawOp, l *squareLine, key uint32, j0 int32, cut bool, c *squareGeom) {
	n := int32(1) << op.Pixel.ScaleShift
	simple := SimpleWrite(&op.Pixel)
	g := l.G
	if l.First > l.Last {
		return
	}
	uz := min(j0+GenChunk-1, l.Last+n-1)
	for u := max(j0, l.First-1); u <= uz; u++ {
		p := squareColumnOf(&g, u, n)
		lo, hi := squareClaims(&g, p, n, l.First, l.Last)
		if lo > hi {
			continue
		}
		if g.Cut > l.Last {
			flo, fhi := squareClaims(&g, p, n, l.Last+1, g.Cut)
			if flo <= fhi {
				if flo <= lo {
					lo = max(lo, fhi+1)
				} else {
					hi = min(hi, flo-1)
				}
			}
		}
		cutLo, cutHi := squareCut(cut, c, p, n)
		putSquareCut(b.FB, b.Round, op, simple, key, g.AxisX, p, lo, hi, cutLo, cutHi, op.Color, 0)
	}
}
