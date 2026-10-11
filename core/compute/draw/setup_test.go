// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

// TestOpBox checks the box per kind: the clip cut, a reversed line,
// an erase's rectangle, an op wholly outside the clip, and the
// physical row under DIE.
func TestOpBox(t *testing.T) {
	p := PixelParams{ClipX: 319, ClipY: 223, FBWidth: 512}
	sprite := DrawOp{Kind: OpNormalSprite, Pixel: p, CharW: 8, CharH: 8}
	sprite.X[0], sprite.Y[0] = -4, -4
	if x0, y0, x1, y1, ok := OpBox(&sprite); !ok || x0 != 0 || y0 != 0 || x1 != 3 || y1 != 3 {
		t.Errorf("sprite box (%d,%d)-(%d,%d) %v, want (0,0)-(3,3)", x0, y0, x1, y1, ok)
	}
	line := DrawOp{Kind: OpLine, Pixel: p}
	line.X[0], line.Y[0], line.X[1], line.Y[1] = 300, 200, 330, 100
	if x0, y0, x1, y1, ok := OpBox(&line); !ok || x0 != 300 || y0 != 100 || x1 != 319 || y1 != 200 {
		t.Errorf("line box (%d,%d)-(%d,%d) %v, want (300,100)-(319,200)", x0, y0, x1, y1, ok)
	}
	erase := DrawOp{Kind: OpErase, Color: 1}
	erase.Pixel.FBWidth, erase.Pixel.FBHeight = 512, 256
	erase.X[0], erase.Y[0], erase.X[2], erase.Y[2] = 8, 4, 400, 250
	if x0, y0, x1, y1, ok := OpBox(&erase); !ok || x0 != 8 || y0 != 4 || x1 != 400 || y1 != 250 {
		t.Errorf("erase box (%d,%d)-(%d,%d) %v, want (8,4)-(400,250)", x0, y0, x1, y1, ok)
	}
	outside := DrawOp{Kind: OpPolygon, Pixel: p}
	outside.X = [4]int32{400, 410, 410, 400}
	outside.Y = [4]int32{0, 0, 10, 10}
	if _, _, _, _, ok := OpBox(&outside); ok {
		t.Error("a polygon past the clip has a box")
	}
	p.DIE = 1
	if got := PhysicalRow(&p, 11); got != 5 {
		t.Errorf("physical row of 11 under DIE = %d, want 5", got)
	}
}

// TestOpReadRanges checks the VRAM ranges an op's texel reads cover:
// the character data by rows of the color mode's stride for every
// sprite kind, the color lookup table in color mode 1 alone, and no
// range for a kind that reads no texels.
func TestOpReadRanges(t *testing.T) {
	cases := []struct {
		name               string
		op                 DrawOp
		charSize, clutAddr uint32
		clutSize           uint32
	}{
		{"normal mode 0", DrawOp{Kind: OpNormalSprite, CharAddr: 0x1000, CharW: 16, CharH: 8, ColorMode: 0, Color: 0x80}, 64, 0, 0},
		{"scaled mode 1", DrawOp{Kind: OpScaledSprite, CharAddr: 0x2000, CharW: 16, CharH: 8, ColorMode: 1, Color: 0x80}, 64, 0x400, 32},
		{"distorted mode 5", DrawOp{Kind: OpDistortedSprite, CharAddr: 0x3000, CharW: 16, CharH: 8, ColorMode: 5, Color: 0x80}, 256, 0, 0},
		{"polygon mode 1", DrawOp{Kind: OpPolygon, CharAddr: 0x4000, CharW: 16, CharH: 8, ColorMode: 1, Color: 0x80}, 0, 0, 0},
		{"erase", DrawOp{Kind: OpErase, Color: 0x80}, 0, 0, 0},
	}
	for _, c := range cases {
		addr, size := OpCharData(&c.op)
		wantAddr := c.op.CharAddr
		if c.charSize == 0 {
			wantAddr = 0
		}
		if addr != wantAddr || size != c.charSize {
			t.Errorf("%s: character data %#x+%d, want %#x+%d", c.name, addr, size, wantAddr, c.charSize)
		}
		if addr, size := OpColorTable(&c.op); addr != c.clutAddr || size != c.clutSize {
			t.Errorf("%s: color table %#x+%d, want %#x+%d", c.name, addr, size, c.clutAddr, c.clutSize)
		}
	}
}

// TestOpChunks checks the chunks an op's threads take apart: an
// erase's lines, a drawn op's lines by its runs of GenChunk steps
// rounded up, and none for an op that draws nothing.
func TestOpChunks(t *testing.T) {
	pixel := PixelParams{FBWidth: 512, FBHeight: 256}
	cases := []struct {
		name string
		op   DrawOp
		want uint32
	}{
		{"erase", DrawOp{Kind: OpErase, X: [4]int32{0, 0, 511, 0}, Y: [4]int32{0, 0, 223, 0}, Pixel: pixel}, 224},
		{"sprite whole chunks", DrawOp{Kind: OpNormalSprite, CharW: 64, CharH: 8}, 16},
		{"sprite partial chunk", DrawOp{Kind: OpNormalSprite, CharW: 72, CharH: 8}, 24},
		{"sprite under a chunk", DrawOp{Kind: OpNormalSprite, CharW: 8, CharH: 8}, 8},
		{"no lines", DrawOp{Kind: OpNormalSprite, CharW: 512, CharH: 8}, 0},
	}
	for _, c := range cases {
		if got := OpChunks(&c.op); got != c.want {
			t.Errorf("%s: %d chunks, want %d", c.name, got, c.want)
		}
	}
}

// TestOpDotsCoversRoundedLines holds OpDots at or above the dots of
// every connecting line of a four-vertex part, for parts whose side
// edges round a half-way position apart: the A-D edge takes the dot
// toward its start and the B-C edge the dot away from it, so a line
// between them can be one dot longer than the longer of A-B and D-C.
// The polygon A=(0,0) B=(31,0) C=(32,10) D=(1,10) has line 5 from
// x 0 to x 32, 33 dots over an A-B edge of 32. The sweep moves B and
// C together so the rounding lands on every line.
func TestOpDotsCoversRoundedLines(t *testing.T) {
	pixel := PixelParams{ClipX: 511, ClipY: 255, FBWidth: 512, FBHeight: 256}
	check := func(op *DrawOp) {
		t.Helper()
		steps := QuadSteps(op)
		dots := OpDots(op)
		for i := int32(0); i <= steps; i++ {
			var st QuadLine
			BeginQuadLine(op, &st, i)
			if st.Len+1 > dots {
				t.Fatalf("kind %d A=(%d,%d) B=(%d,%d) C=(%d,%d) D=(%d,%d) line %d: %d dots, OpDots %d",
					op.Kind, op.X[0], op.Y[0], op.X[1], op.Y[1], op.X[2], op.Y[2], op.X[3], op.Y[3], i, st.Len+1, dots)
			}
		}
	}
	for _, kind := range []uint32{OpPolygon, OpDistortedSprite} {
		for bx := int32(24); bx <= 40; bx++ {
			for h := int32(2); h <= 12; h++ {
				op := DrawOp{Kind: kind, Pixel: pixel, CharW: 32, CharH: 32}
				op.X = [4]int32{0, bx, bx + 1, 1}
				op.Y = [4]int32{0, 0, h, h}
				check(&op)
				op.X = [4]int32{1, bx + 1, bx, 0}
				check(&op)
			}
		}
	}
	op := DrawOp{Kind: OpPolygon, Pixel: pixel}
	op.X = [4]int32{0, 31, 32, 1}
	op.Y = [4]int32{0, 0, 10, 10}
	var st QuadLine
	BeginQuadLine(&op, &st, 5)
	if st.LX != 0 || st.RX != 32 || st.Len != 32 {
		t.Fatalf("line 5 runs x %d to %d over %d steps, want 0 to 32 over 32", st.LX, st.RX, st.Len)
	}
	check(&op)
}

// TestPhysicalRowScaled checks the physical row at scale: under DIE
// sub-row k of native logical row yl, at N*yl + k, lands on sub-row k
// of the native row's physical row, N*(yl>>1) + k; without DIE the
// row is itself.
func TestPhysicalRowScaled(t *testing.T) {
	for shift := uint32(1); shift <= 3; shift++ {
		n := int32(1) << shift
		die := PixelParams{DIE: 1, ScaleShift: shift}
		flat := PixelParams{ScaleShift: shift}
		for yl := int32(0); yl < 16; yl++ {
			for k := range n {
				y := n*yl + k
				if got, want := PhysicalRow(&die, y), n*(yl>>1)+k; got != want {
					t.Fatalf("scale %d DIE: row %d = %d, want %d", n, y, got, want)
				}
				if got := PhysicalRow(&flat, y); got != y {
					t.Fatalf("scale %d: row %d = %d, want itself", n, y, got)
				}
			}
		}
	}
}
