// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

// PreClipReject reports whether a bounding box lies entirely outside
// the drawing area (0,0)-(clipX,clipY).
func PreClipReject(minX, minY, maxX, maxY, clipX, clipY int32) bool {
	return maxX < 0 || minX > clipX || maxY < 0 || minY > clipY
}

// clipBound is a parameter value along a segment as a fraction with a
// positive denominator.
type clipBound struct {
	n, d int64
}

// less reports a/b < c/d for positive denominators.
func (a clipBound) less(b clipBound) bool {
	return a.n*b.d < b.n*a.d
}

// ClipSteps intersects the segment (x0,y0)-(x1,y1) with the drawing
// area [0,clipX] x [0,clipY] (Liang-Barsky) and returns the visible
// parameter range scaled to the segment's steps: j0 and j1 are the
// floors of t0 and t1 times lineLen, with t0 and t1 the entry and
// exit parameters in [0,1]. ok is false when the segment lies wholly
// outside. The rasterizers skip connecting-line dots outside [j0, j1]
// with a slack margin (VDP1 manual pre-clipping: for lines completely
// separated from the drawing area drawing does not start); output is
// unchanged because the pixel write discards those dots anyway.
//
// The parameters are exact rationals: t0 is the largest entry
// fraction and t1 the smallest exit fraction, compared by cross
// multiplication, so the floors are exact.
func ClipSteps(x0, y0, x1, y1, clipX, clipY, lineLen int32) (j0, j1 int32, ok bool) {
	dx := int64(x1 - x0)
	dy := int64(y1 - y0)
	// Each edge of the area as (p, q): the segment enters where p < 0
	// and exits where p > 0 at parameter q/p.
	var ps, qs [4]int64
	ps[0], qs[0] = -dx, int64(x0)
	ps[1], qs[1] = dx, int64(clipX-x0)
	ps[2], qs[2] = -dy, int64(y0)
	ps[3], qs[3] = dy, int64(clipY-y0)
	t0 := clipBound{0, 1}
	t1 := clipBound{1, 1}
	for i := range int32(4) {
		p, q := ps[i], qs[i]
		if p == 0 {
			if q < 0 {
				return 0, 0, false
			}
			continue
		}
		r := clipBound{q, p}
		if p < 0 {
			r.n, r.d = -q, -p
			if t1.less(r) {
				return 0, 0, false
			}
			if t0.less(r) {
				t0 = r
			}
		} else {
			if r.less(t0) {
				return 0, 0, false
			}
			if r.less(t1) {
				t1 = r
			}
		}
	}
	l := int64(lineLen)
	return int32(floorDiv64(t0.n*l, t0.d)), int32(floorDiv64(t1.n*l, t1.d)), true
}
