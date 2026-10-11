// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
)

// squareModel draws op at the scale shift by the rule generate
// implements per column (square.go), written out dot by dot: every
// dot of the scaled walk, hole fills included, claims the N by N
// square on its + sides in the walk's order, a later dot's claim
// replacing an earlier one's, and after the whole op each sub-dot a
// drawn dot claims takes that dot's value over the frame buffer. The
// walk is the connecting line setup and closed forms the walk itself
// uses on the scaled op, with the texel rules and Gouraud values
// taken at each step.
func squareModel(vram *compute.VDP1VRAM, fb compute.VDP1FB, op *draw.DrawOp) {
	shift := op.Pixel.ScaleShift
	n := int32(1) << shift
	w, h := op.Pixel.FBWidth, op.Pixel.FBHeight
	owner := make([]int32, w*h)
	pixels := make([]uint32, 0, 1<<16)
	gouraud := make([]uint32, 0, 1<<16)
	claim := func(x, y int32, id int32) {
		for dy := range n {
			for dx := range n {
				if cx, cy := x+dx, y+dy; cx >= 0 && cx < w && cy >= 0 && cy < h {
					owner[cy*w+cx] = id
				}
			}
		}
	}
	dot := func(x, y int32, drawn bool, pixel, g uint32) {
		id := int32(0)
		if drawn {
			pixels = append(pixels, pixel)
			gouraud = append(gouraud, g)
			id = int32(len(pixels))
		}
		claim(x, y, id)
	}
	steps := draw.QuadSteps(op)
	for i := int32(0); i <= steps; i++ {
		var st draw.QuadLine
		draw.BeginQuadLine(op, &st, i)
		tex := op.Kind == draw.OpDistortedSprite
		prevX, prevY := st.LX, st.LY
		for j := int32(0); j <= st.Len; j++ {
			x, y := draw.QuadPos(&st, j)
			drawn, pixel := true, op.Color
			if tex {
				u := int32(int64(st.UStart)<<(16+shift) + int64(st.DU)*int64(j))
				srcU := draw.QuadSrcU(op, &st, u>>(16+shift))
				d := draw.RowDotWord(vram, st.RowBase, srcU)
				var ec draw.EndCodeCounter
				drawn = draw.TexelDraws(d, op.ColorMode, op.Flags&draw.OpFlagSPDOn, st.CheckECD, &ec, j)
				pixel = draw.DotToPixel(vram, d, op.Color, op.ColorMode)
			}
			g := uint32(0)
			if op.Pixel.CC >= 4 {
				gs := st.Gouraud
				gs.Advance(j)
				g = gs.Value(shift)
			}
			if draw.HoleFills(j, x, y, prevX, prevY) {
				hx, hy := draw.HoleFillDot(x, y, prevX, prevY)
				dot(hx, hy, drawn, pixel, g)
			}
			dot(x, y, drawn, pixel, g)
			prevX, prevY = x, y
		}
	}
	for y := range h {
		for x := range w {
			id := owner[y*w+x]
			if id == 0 || !draw.Admits(&op.Pixel, x, y) {
				continue
			}
			index := uint32(y*w + x)
			dst := uint32(fb.U16BE(index * 2))
			if out := draw.Combine(&op.Pixel, dst, pixels[id-1], gouraud[id-1]); out != draw.NoStore {
				fb.PutU16BE(index*2, uint16(out))
			}
		}
	}
}

// TestScaleOpQuadsMatchSquareModel draws fans of polygons sharing
// edges, flat and Gouraud shaded, in every vertex order, a slanted
// distorted sprite whose texture has transparent dots, flat and half
// transparent, and a bowtie polygon, at scale shifts 1 to 3 through
// generate, and holds each frame buffer to the square model's. The
// fans are three polygons meeting at a tip, whose shared edges are
// formed by one polygon's line ends and the other's first or last
// line, the configuration the extension model drew apart.
func TestScaleOpQuadsMatchSquareModel(t *testing.T) {
	const fbW, fbH, charW, charH = 128, 64, 16, 8
	vram := new(compute.VDP1VRAM)
	for r := range charH {
		for c := range charW {
			px := uint16(0x8000 | (c*37+r*101)<<5&0x7FE0 | (c+r)&0x1F | 1)
			if (c+r)%5 == 0 || c < 2 || r >= charH-2 {
				px = 0
			}
			vram[(r*charW+c)*2], vram[(r*charW+c)*2+1] = uint8(px>>8), uint8(px)
		}
	}
	fan := func(cc uint32, order int) []draw.DrawOp {
		// The vertices of a fan of three triangles meeting at a tip, a
		// polygon's A and D coinciding, in the emblem's proportions.
		tris := [3][3][2]int32{
			{{62, 20}, {63, 16}, {50, 31}},
			{{62, 47}, {61, 37}, {50, 31}},
			{{61, 37}, {62, 20}, {50, 31}},
		}
		colors := [3]uint32{0xFE00, 0x801F, 0xFC10}
		var ops []draw.DrawOp
		for k := range tris {
			op := draw.DrawOp{Kind: draw.OpPolygon, Color: colors[k], Gouraud: [4]uint32{0x0210, 0x7C1F, 0x03E0, 0x4A52}}
			op.Pixel = draw.PixelParams{CC: cc}
			v := tris[k]
			corners := [4][2]int32{v[0], v[1], v[2], v[0]}
			for c := range 4 {
				o := (order + c) % 4
				if order >= 4 {
					o = (order - c + 4) % 4
				}
				op.X[c], op.Y[c] = corners[o][0], corners[o][1]
			}
			ops = append(ops, op)
		}
		return ops
	}
	type scene struct {
		name string
		ops  []draw.DrawOp
	}
	var scenes []scene
	for order := range 8 {
		scenes = append(scenes, scene{fmt.Sprintf("flat fan order %d", order), fan(0, order)})
		scenes = append(scenes, scene{fmt.Sprintf("shaded fan order %d", order), fan(4, order)})
	}
	sprite := func(cc uint32, x, y [4]int32) draw.DrawOp {
		op := draw.DrawOp{Kind: draw.OpDistortedSprite, CharW: charW, CharH: charH, ColorMode: 5, Flags: draw.OpFlagECDOff, X: x, Y: y}
		op.Pixel = draw.PixelParams{CC: cc}
		return op
	}
	scenes = append(scenes,
		scene{"slanted sprite", []draw.DrawOp{sprite(0, [4]int32{10, 70, 60, 8}, [4]int32{6, 10, 50, 40})}},
		scene{"slanted sprite walking -", []draw.DrawOp{sprite(0, [4]int32{70, 10, 8, 60}, [4]int32{10, 6, 40, 50})}},
		scene{"half transparent sprite over a fan", append(fan(0, 0), sprite(3, [4]int32{40, 90, 100, 45}, [4]int32{10, 14, 50, 44}))},
		scene{"bowtie polygon", []draw.DrawOp{{Kind: draw.OpPolygon, Color: 0x83E5, X: [4]int32{10, 60, 10, 60}, Y: [4]int32{10, 40, 40, 10}}}},
		// Parts reaching past the clip on every side, whose off-screen
		// steps the pre-clip cuts and whose squares at the edges land.
		scene{"sprite over the top-left edges", []draw.DrawOp{sprite(0, [4]int32{-20, 40, 30, -25}, [4]int32{-9, -3, 30, 26})}},
		scene{"polygon over the bottom-right edges", []draw.DrawOp{{Kind: draw.OpPolygon, Color: 0xFC10, X: [4]int32{100, 150, 140, 90}, Y: [4]int32{40, 45, 80, 70}}}},
		scene{"sprite far off screen", []draw.DrawOp{sprite(0, [4]int32{200, 260, 250, 190}, [4]int32{-50, -40, 10, 0})}},
		// A thin polygon whose connecting lines run thousands of dots
		// from off screen, so at 8x the steps landing on screen are
		// past the 32-bit range of the position step's product.
		scene{"polygon from far off screen", []draw.DrawOp{{Kind: draw.OpPolygon, Color: 0x83E5, X: [4]int32{-4000, 1000, 1000, -4000}, Y: [4]int32{-4000, 1000, 1004, -3996}}}},
	)
	for _, s := range scenes {
		for shift := uint32(1); shift <= 3; shift++ {
			fbs := [2]compute.VDP1FB{make(compute.VDP1FB, fbW*fbH*2<<(2*shift)), make(compute.VDP1FB, fbW*fbH*2<<(2*shift))}
			for i := range s.ops {
				op := s.ops[i]
				op.Pixel.ClipX, op.Pixel.ClipY, op.Pixel.FBWidth, op.Pixel.FBHeight = fbW-1, fbH-1, fbW, fbH
				rec := walkRecords(vram, make(compute.VDP1FB, fbW*fbH*2), &op)
				var scaled draw.DrawOp
				scaleOp(&scaled, &op, shift)
				draw.Generate(&draw.Buffers{VRAM: vram, FB: fbs[0], Records: rec}, &scaled, 0, 1)
				squareModel(vram, fbs[1], &scaled)
			}
			if !bytes.Equal(fbs[0], fbs[1]) {
				i := firstDiff(fbs[0], fbs[1]) / 2
				sw := fbW << shift
				t.Fatalf("%s shift %d: sub-dot (%d, %d) is %04x, the model has %04x", s.name, shift, i%sw, i/sw, fbs[0].U16BE(uint32(i)*2), fbs[1].U16BE(uint32(i)*2))
			}
		}
	}
}
