// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestQuadLineScaledCornerSamples holds a scaled axis-aligned
// distorted sprite's samples to the native sprite's at every block
// corner, over a sweep of widths, heights, and character sizes, both
// flips, at scale shifts 1 to 3: sub-line N*i runs between N times
// native line i's endpoints, and its step N*j reads native step j's
// texture row and column and holds its Gouraud value. The texture
// positions and the Gouraud values carry the scale's fraction bits,
// so the sub-line steps by the native deltas. A step divided by N
// instead reads another row, column, or level where a native position
// lies just past one: a 3 by 3 character over 140 dots and a Gouraud
// difference of 2 over 140 dots at 8x are such cases (the second
// Gouraud table, taken with the flipped sprites).
func TestQuadLineScaledCornerSamples(t *testing.T) {
	pixel := PixelParams{CC: 4, ClipX: 4095, ClipY: 4095, FBWidth: 512, FBHeight: 256}
	widths := []int32{140}
	for w := int32(1); w <= 90; w += 3 {
		widths = append(widths, w)
	}
	gouraud := [][4]uint32{{0x0210, 0x7C1F, 0x03E0, 0x4A52}, {0x0000, 0x0002, 0x0002, 0x0000}}
	checked := 0
	for _, char := range [][2]int32{{3, 3}, {8, 4}, {24, 7}, {64, 16}, {504, 255}} {
		for fi, flags := range []uint32{0, OpFlagFlipH | OpFlagFlipV} {
			for _, w := range widths {
				for _, h := range []int32{1, 2, 5, 13, 40, 140} {
					native := DrawOp{Kind: OpDistortedSprite, CharW: char[0], CharH: char[1], ColorMode: 5,
						Flags: flags | OpFlagECDOff, Gouraud: gouraud[fi], Pixel: pixel}
					native.X = [4]int32{3, 3 + w - 1, 3 + w - 1, 3}
					native.Y = [4]int32{2, 2, 2 + h - 1, 2 + h - 1}
					steps := QuadSteps(&native)
					for shift := uint32(1); shift <= 3; shift++ {
						n := int32(1) << shift
						scaled := native
						for k := range 4 {
							scaled.X[k] *= n
							scaled.Y[k] *= n
						}
						scaled.Pixel.ScaleShift = shift
						for i := range steps + 1 {
							var nst, st QuadLine
							BeginQuadLine(&native, &nst, i)
							BeginQuadLine(&scaled, &st, n*i)
							if st.RowBase != nst.RowBase {
								t.Fatalf("char %v flags %#x %dx%d shift %d line %d: row base %#x, want %#x", char, flags, w, h, shift, i, st.RowBase, nst.RowBase)
							}
							for j := range nst.Len + 1 {
								got := QuadSrcU(&scaled, &st, quadU(&st, n*j, shift)>>(16+shift))
								want := QuadSrcU(&native, &nst, quadU(&nst, j, 0)>>16)
								if got != want {
									t.Fatalf("char %v flags %#x %dx%d shift %d line %d step %d: column %d, want %d", char, flags, w, h, shift, i, j, got, want)
								}
								g, ng := st.Gouraud, nst.Gouraud
								g.Advance(n * j)
								ng.Advance(j)
								if g.Value(shift) != ng.Value(0) {
									t.Fatalf("char %v flags %#x %dx%d shift %d line %d step %d: Gouraud %#04x, want %#04x", char, flags, w, h, shift, i, j, g.Value(shift), ng.Value(0))
								}
								checked++
							}
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Error("nothing checked")
	}
}

// TestQuadLineNativeDecisions holds a scaled distorted sprite's line
// decisions to the native line's: every scaled line decides high
// speed shrink and the end code check as native line L >> ScaleShift
// of the native op does, since the rules compare the native line's
// length with the character width. quadLineEnds, which rebuilds the
// native line, gives the endpoints BeginQuadLine sets.
func TestQuadLineNativeDecisions(t *testing.T) {
	for _, flags := range []uint32{OpFlagHSS, OpFlagHSS | OpFlagECDOff, 0} {
		native := DrawOp{Kind: OpDistortedSprite, CharW: 32, CharH: 16, ColorMode: 5, Flags: flags}
		native.X = [4]int32{0, 40, 20, 5}
		native.Y = [4]int32{0, 0, 20, 20}
		native.Pixel = PixelParams{ClipX: 1023, ClipY: 1023, FBWidth: 512, FBHeight: 256}
		steps := QuadSteps(&native)
		for l := range steps + 1 {
			var st QuadLine
			BeginQuadLine(&native, &st, l)
			if lx, ly, rx, ry := quadLineEnds(&native, steps, l); lx != st.LX || ly != st.LY || rx != st.RX || ry != st.RY {
				t.Fatalf("line %d: quadLineEnds (%d,%d)-(%d,%d), BeginQuadLine (%d,%d)-(%d,%d)", l, lx, ly, rx, ry, st.LX, st.LY, st.RX, st.RY)
			}
		}
		for shift := uint32(1); shift <= 3; shift++ {
			n := int32(1) << shift
			scaled := native
			for k := range 4 {
				scaled.X[k] *= n
				scaled.Y[k] *= n
			}
			scaled.Pixel.ScaleShift = shift
			if QuadSteps(&scaled) != n*steps {
				t.Fatalf("shift %d: scaled steps %d, want %d", shift, QuadSteps(&scaled), n*steps)
			}
			shrinks := 0
			for l := range n*steps + 1 {
				var sst, nst QuadLine
				BeginQuadLine(&scaled, &sst, l)
				BeginQuadLine(&native, &nst, l>>shift)
				if sst.HSSShrink != nst.HSSShrink || sst.CheckECD != nst.CheckECD {
					t.Fatalf("flags %#x shift %d line %d: shrink %v check %v, want the native line's %v %v", flags, shift, l, sst.HSSShrink, sst.CheckECD, nst.HSSShrink, nst.CheckECD)
				}
				if sst.HSSShrink {
					shrinks++
				}
			}
			if flags&OpFlagHSS != 0 && (shrinks == 0 || shrinks == int(n*steps+1)) {
				t.Fatalf("flags %#x shift %d: %d of %d lines shrink, want some and not all", flags, shift, shrinks, n*steps+1)
			}
		}
	}
}

// endCodeRow writes a 16-dot RGB character row at address 0: dot i is
// 0x8001+i except the end codes at dots 3 and 6.
func endCodeRow() *compute.VDP1VRAM {
	vram := new(compute.VDP1VRAM)
	for i := range uint32(16) {
		dot := uint16(0x8001 + i)
		if i == 3 || i == 6 {
			dot = 0x7FFF
		}
		vram[i*2], vram[i*2+1] = uint8(dot>>8), uint8(dot)
	}
	return vram
}

// checkEndCodeRow checks row 0 of fb after a 16-dot draw of
// endCodeRow: the end code dots never drawn, the dots before the
// second end code drawn, and the dots after it drawn only when the
// counter does not cut the line.
func checkEndCodeRow(t *testing.T, name string, fb compute.VDP1FB, cut bool) {
	t.Helper()
	for x := range uint32(16) {
		got := fb.U16BE(x * 2)
		want := uint16(0x8001 + x)
		switch {
		case x == 3 || x == 6:
			want = 0
		case x > 6 && cut:
			want = 0
		}
		if got != want {
			t.Fatalf("%s: dot %d = %#04x, want %#04x", name, x, got, want)
		}
	}
}

// TestEndCodesCutOnlyAtNative checks the end code counter at scale: a
// sprite row and a distorted sprite line with two end codes are cut
// at the second at native, and at scale the end code dots are still
// skipped but the counter does not cut the line.
func TestEndCodesCutOnlyAtNative(t *testing.T) {
	vram := endCodeRow()
	for shift := uint32(0); shift <= 1; shift++ {
		cut := shift == 0
		pixel := PixelParams{ClipX: 511, ClipY: 255, FBWidth: 512, FBHeight: 256, ScaleShift: shift}

		sprite := DrawOp{Kind: OpNormalSprite, CharW: 16, CharH: 1, ColorMode: 5, Pixel: pixel}
		fb := make(compute.VDP1FB, compute.VDP1FBSize)
		var row SpriteRow
		BeginSpriteRow(&sprite, &row, 0)
		SpriteRowSpan(vram, fb, nil, &sprite, &row, 0, 16, UnboundedCycles, 1)
		checkEndCodeRow(t, "sprite row shift "+string(rune('0'+shift)), fb, cut)

		quad := DrawOp{Kind: OpDistortedSprite, CharW: 16, CharH: 1, ColorMode: 5, Pixel: pixel}
		quad.X = [4]int32{0, 15, 15, 0}
		fb = make(compute.VDP1FB, compute.VDP1FBSize)
		var line QuadLine
		BeginQuadLine(&quad, &line, 0)
		QuadLineSpan(vram, fb, nil, &quad, &line, line.JEnd+1, UnboundedCycles, 1)
		checkEndCodeRow(t, "distorted line shift "+string(rune('0'+shift)), fb, cut)
	}
}
