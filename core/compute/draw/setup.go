// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

// absI32 is the absolute value of x.
func absI32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

// QuadSteps is the number of edge steps of a four-vertex part (a
// distorted sprite or a polygon): the larger Chebyshev length of the
// A-D and B-C side edges. Zero when both side edges are points.
func QuadSteps(op *DrawOp) int32 {
	ad := absI32(op.X[3] - op.X[0])
	ady := absI32(op.Y[3] - op.Y[0])
	if ady > ad {
		ad = ady
	}
	bc := absI32(op.X[2] - op.X[1])
	bcy := absI32(op.Y[2] - op.Y[1])
	if bcy > bc {
		bc = bcy
	}
	if bc > ad {
		return bc
	}
	return ad
}

// SimpleWrite reports whether the pixel write is a plain 16-bit
// replace gated by the system clip alone: no color calculation, MSB
// set, mesh, user clip, double interlace, or 8-bit frame buffer, in
// the walk's store pass. A rasterizer may then store the pixel
// directly.
func SimpleWrite(p *PixelParams) bool {
	return p.DIE == 0 && p.CC == 0 && p.MSBOn == 0 && p.Mesh == 0 && p.UserClip == 0 && !p.Is8bpp && p.Pass == PassStore
}

// RowStride is the bytes per character row for the color mode: 4 bits
// per dot in modes 0 and 1, a byte in modes 2 to 4, a word in mode 5.
func RowStride(charW int32, colorMode uint32) int32 {
	if colorMode <= 1 {
		return charW / 2
	}
	if colorMode == 5 {
		return charW * 2
	}
	return charW
}

// OpCharData is the VRAM range op's texel reads cover: CharH rows of
// RowStride bytes from CharAddr, the character data a sprite of any
// kind reads. A kind that reads no texels has a zero size.
func OpCharData(op *DrawOp) (addr, size uint32) {
	switch op.Kind {
	case OpNormalSprite, OpScaledSprite, OpDistortedSprite:
		return op.CharAddr, uint32(op.CharH * RowStride(op.CharW, op.ColorMode))
	}
	return 0, 0
}

// OpColorTable is the VRAM range of op's color lookup table, the 16
// words at CMDCOLR times 8 that DotToPixel reads in color mode 1 for
// a sprite of any kind, and a zero size in every other mode or for a
// kind that reads no texels.
func OpColorTable(op *DrawOp) (addr, size uint32) {
	switch op.Kind {
	case OpNormalSprite, OpScaledSprite, OpDistortedSprite:
		if op.ColorMode == 1 {
			return op.Color * 8, 32
		}
	}
	return 0, 0
}

// DistortedTexture is a distorted sprite's texture walk: V steps
// with the edges in fixed point, 16 fraction bits plus the op's
// scale shift, across the part's edge steps, and every connecting
// line walks U from UStart to UEnd. The flips reverse the ranges.
type DistortedTexture struct {
	VStart int32
	VStep  int32
	UStart int32
	UEnd   int32
}

// NewDistortedTexture is the texture walk of op over steps edge
// steps. Over N times the native steps at the scale shift of N the V
// step is the native step, so edge step N*i reads the native row of
// edge step i.
func NewDistortedTexture(op *DrawOp, steps int32) DistortedTexture {
	var t DistortedTexture
	vStart := int32(0)
	vEnd := op.CharH - 1
	if op.Flags&OpFlagFlipV != 0 {
		vStart, vEnd = vEnd, vStart
	}
	frac := 16 + op.Pixel.ScaleShift
	t.VStart = vStart << frac
	if steps != 0 {
		t.VStep = ((vEnd - vStart) << frac) / steps
	}
	t.UStart = 0
	t.UEnd = op.CharW - 1
	if op.Flags&OpFlagFlipH != 0 {
		t.UStart, t.UEnd = t.UEnd, t.UStart
	}
	return t
}

// nativeCoordLimit bounds a vertex coordinate at native scale: 13-bit
// command coordinates plus 16-bit local coordinates stay under 40960,
// so a coordinate at or past 1 << 16 is not one the walk produced. A
// scaled op's coordinates are its native ones shifted left by its
// scale shift, so they are bounded shifted back.
const nativeCoordLimit = 1 << 16

// nativeOp is the native op a scaled op was derived from: the
// vertices shifted right by ScaleShift, a scaled sprite's inclusive
// lower-right corner and the system clip inverted from N * (v + 1) -
// 1, and the pixel params holding only the clip, so the native op's
// lines set up as the walk set them up, with no Gouraud stepping.
func nativeOp(op *DrawOp) DrawOp {
	shift := op.Pixel.ScaleShift
	native := *op
	for k := range int32(4) {
		native.X[k] = op.X[k] >> shift
		native.Y[k] = op.Y[k] >> shift
	}
	if op.Kind == OpScaledSprite {
		native.X[2] = (op.X[2]+1)>>shift - 1
		native.Y[2] = (op.Y[2]+1)>>shift - 1
	}
	native.Pixel = PixelParams{
		ClipX: (op.Pixel.ClipX+1)>>shift - 1,
		ClipY: (op.Pixel.ClipY+1)>>shift - 1,
	}
	return native
}

// OpLines is the lines a rasterizer draws for op: the rows of a
// sprite, the connecting lines of a four-vertex part (its edge steps
// plus one, and one for a polygon whose side edges are points, drawn
// as a segment), the four legs of a polyline, the one segment of a
// line, or the rows of an erase rectangle. An op whose vertices or
// character size lie outside what the walk can produce draws
// nothing: a renderer reading the op as buffer content must not run
// the loops it bounds on a corrupt record.
func OpLines(op *DrawOp) int32 {
	shift := op.Pixel.ScaleShift
	for i := range int32(4) {
		if absI32(op.X[i])>>shift >= nativeCoordLimit || absI32(op.Y[i])>>shift >= nativeCoordLimit {
			return 0
		}
	}
	if op.CharW > 504 || op.CharH > 255 || op.CharW < 0 || op.CharH < 0 {
		return 0
	}
	switch op.Kind {
	case OpNormalSprite, OpScaledSprite:
		return SpriteRows(op)
	case OpDistortedSprite, OpPolygon:
		return QuadSteps(op) + 1
	case OpPolyline:
		return 4
	case OpErase:
		return eraseRows(op)
	}
	return 1
}

// OpForm is the loop a rasterizer runs for op: its kind, except that
// a polygon whose side edges are points is the A-B segment, the line
// form.
func OpForm(op *DrawOp) uint32 {
	if op.Kind == OpPolygon && QuadSteps(op) == 0 {
		return OpLine
	}
	return op.Kind
}

// eraseRows is the rows of an erase op's rectangle, zero unless the
// rectangle lies inside the frame buffer the op describes: its width
// and its height in rows.
func eraseRows(op *DrawOp) int32 {
	if op.Pixel.FBWidth <= 0 || eraseCols(op) == 0 {
		return 0
	}
	if op.Y[0] < 0 || op.Y[2] < op.Y[0] || op.Y[2] >= op.Pixel.FBHeight {
		return 0
	}
	return op.Y[2] - op.Y[0] + 1
}

// eraseCols is the columns of an erase op's rectangle, zero unless
// the rectangle's columns lie inside the frame buffer's width.
func eraseCols(op *DrawOp) int32 {
	if op.X[0] < 0 || op.X[2] < op.X[0] || op.X[2] >= op.Pixel.FBWidth {
		return 0
	}
	return op.X[2] - op.X[0] + 1
}

// QuadBounds is the bounding box of the four vertices.
func QuadBounds(op *DrawOp) (minX, minY, maxX, maxY int32) {
	minX, maxX = op.X[0], op.X[0]
	minY, maxY = op.Y[0], op.Y[0]
	for i := int32(1); i < 4; i++ {
		if op.X[i] < minX {
			minX = op.X[i]
		}
		if op.X[i] > maxX {
			maxX = op.X[i]
		}
		if op.Y[i] < minY {
			minY = op.Y[i]
		}
		if op.Y[i] > maxY {
			maxY = op.Y[i]
		}
	}
	return minX, minY, maxX, maxY
}

// OpBox is the inclusive box of the dots op can store, in the op's
// own coordinates (logical rows under DIE): a draw's vertex or
// character box cut to the system clip, an erase's rectangle. ok is
// false when nothing can land.
func OpBox(op *DrawOp) (x0, y0, x1, y1 int32, ok bool) {
	if OpLines(op) == 0 {
		return 0, 0, 0, 0, false
	}
	switch op.Kind {
	case OpNormalSprite:
		x0, y0 = op.X[0], op.Y[0]
		x1, y1 = op.X[0]+op.CharW-1, op.Y[0]+op.CharH-1
	case OpScaledSprite, OpErase:
		x0, y0 = op.X[0], op.Y[0]
		x1, y1 = op.X[2], op.Y[2]
	case OpLine:
		x0, x1 = op.X[0], op.X[1]
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		y0, y1 = op.Y[0], op.Y[1]
		if y1 < y0 {
			y0, y1 = y1, y0
		}
	default:
		x0, y0, x1, y1 = QuadBounds(op)
	}
	if op.Kind == OpErase {
		return x0, y0, x1, y1, true
	}
	if n := int32(1) << op.Pixel.ScaleShift; n > 1 && op.Kind != OpNormalSprite && op.Kind != OpScaledSprite {
		// At scale each dot of a connecting line or a segment covers
		// its N by N square on its + sides (square.go).
		x1, y1 = x1+n-1, y1+n-1
	}
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > op.Pixel.ClipX {
		x1 = op.Pixel.ClipX
	}
	if y1 > op.Pixel.ClipY {
		y1 = op.Pixel.ClipY
	}
	if x1 < x0 || y1 < y0 {
		return 0, 0, 0, 0, false
	}
	return x0, y0, x1, y1, true
}

// PhysicalRow is the frame buffer row a dot at logical y lands on:
// the row itself, or its half under DIE, where the game's doubled Y
// halves to the physical row. At scale the halving is of the native
// row, y shifted right by ScaleShift, and the sub-row within the
// native row is kept.
func PhysicalRow(p *PixelParams, y int32) int32 {
	if p.DIE != 0 {
		return y>>(p.ScaleShift+1)<<p.ScaleShift | y&(1<<p.ScaleShift-1)
	}
	return y
}

// chebyshev is the larger axis distance between two vertices of op.
func chebyshev(op *DrawOp, a, b int32) int32 {
	d := absI32(op.X[b] - op.X[a])
	dy := absI32(op.Y[b] - op.Y[a])
	if dy > d {
		return dy
	}
	return d
}

// OpDots is the most dots any line of op draws along its major axis: a
// sprite row's width; for a four-vertex part the longer of the A-B
// and D-C connecting lines plus two, since the lines between
// interpolate those two, whose larger axis distance is convex, and
// their endpoints are the rounded edge positions, the A-D edge
// taking a half-way position toward its start and the B-C edge away
// from it (NewDDAEdge, NewRightDDAEdge), which can place the ends one
// dot further apart than the exact positions; the longest polyline
// leg plus one; the line's dots; an erase rectangle's columns. At
// scale a connecting line or a segment is drawn per column of its
// major axis and reaches N - 1 columns past its last dot (square.go).
func OpDots(op *DrawOp) int32 {
	ext := int32(1)<<op.Pixel.ScaleShift - 1
	switch op.Kind {
	case OpErase:
		if eraseRows(op) == 0 {
			return 0
		}
		return eraseCols(op)
	case OpNormalSprite, OpScaledSprite:
		return SpriteRowWidth(op)
	case OpDistortedSprite, OpPolygon:
		d := chebyshev(op, 0, 1)
		dc := chebyshev(op, 3, 2)
		if dc > d {
			d = dc
		}
		return d + 2 + ext
	case OpPolyline:
		d := chebyshev(op, 0, 1)
		for leg := int32(1); leg < 4; leg++ {
			l := chebyshev(op, leg, (leg+1)&3)
			if l > d {
				d = l
			}
		}
		return d + 1 + ext
	}
	return chebyshev(op, 0, 1) + 1 + ext
}
