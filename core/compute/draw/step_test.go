// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

// TestTexelDraws checks the texel rules: a plain dot draws, a zero
// dot is transparent unless SPD draws it, the end code value is an
// ordinary dot when the line does not check end codes, an end code
// is not drawn and counts when it does, and the second end code
// counted ends the line through the counter.
func TestTexelDraws(t *testing.T) {
	var ec EndCodeCounter
	ec.Reset()
	if !TexelDraws(0x12, 2, 0, true, &ec, 0) {
		t.Error("a plain dot is not drawn")
	}
	if TexelDraws(0, 2, 0, true, &ec, 1) {
		t.Error("a zero dot drew with SPD off")
	}
	if !TexelDraws(0, 2, OpFlagSPDOn, true, &ec, 1) {
		t.Error("a zero dot did not draw with SPD on")
	}
	if !TexelDraws(0xFF, 2, OpFlagSPDOn, false, &ec, 2) {
		t.Error("the end code value did not draw with end codes unchecked")
	}
	if ec.Count != 0 {
		t.Errorf("an unchecked end code counted: %d", ec.Count)
	}
	if TexelDraws(0xFF, 2, OpFlagSPDOn, true, &ec, 3) || ec.Ended() {
		t.Error("the first end code drew or ended the line")
	}
	if TexelDraws(0xFF, 2, OpFlagSPDOn, true, &ec, 3) || ec.Ended() {
		t.Error("the first end code's column re-read drew or ended the line")
	}
	if TexelDraws(0xFF, 2, OpFlagSPDOn, true, &ec, 4) || !ec.Ended() {
		t.Error("the second end code drew or did not end the line")
	}
	if TexelDraws(0xF, 0, 0, true, &ec, 9) {
		t.Error("the 4-bit end code drew")
	}
}

// TestQuadHoleFill checks the hole fill: none at step 0 or when the
// dot shares a coordinate with the previous one, and otherwise the
// corner VDP1 manual Figure 1.6 gives for the step's direction, (px,
// prevPy) when x and y advance the same way and (prevPx, py) when
// they advance opposite ways.
func TestQuadHoleFill(t *testing.T) {
	cases := []struct {
		name                    string
		j, px, py, prevX, prevY int32
		gfx, gfy                int32
		ok                      bool
	}{
		{"step 0", 0, 5, 5, 4, 4, 0, 0, false},
		{"same column", 3, 5, 6, 5, 5, 0, 0, false},
		{"same row", 3, 6, 5, 5, 5, 0, 0, false},
		{"+x +y", 3, 6, 6, 5, 5, 6, 5, true},
		{"-x +y", 3, 4, 6, 5, 5, 5, 6, true},
		{"-x -y", 3, 4, 4, 5, 5, 4, 5, true},
		{"+x -y", 3, 6, 4, 5, 5, 5, 4, true},
	}
	for _, c := range cases {
		ok := HoleFills(c.j, c.px, c.py, c.prevX, c.prevY)
		gfx, gfy := int32(0), int32(0)
		if ok {
			gfx, gfy = HoleFillDot(c.px, c.py, c.prevX, c.prevY)
		}
		if gfx != c.gfx || gfy != c.gfy || ok != c.ok {
			t.Errorf("%s: (%d,%d) %v, want (%d,%d) %v", c.name, gfx, gfy, ok, c.gfx, c.gfy, c.ok)
		}
	}
}

// TestSpriteSrcX checks the source column mappings: a normal row
// reads its column, from the right under a flip; a scaled row
// midpoint-samples the character, flipped, and snaps to one parity
// under high speed shrink.
func TestSpriteSrcX(t *testing.T) {
	normal := DrawOp{Kind: OpNormalSprite, CharW: 16, CharH: 8}
	if NormalSrcX(&normal, 3) != 3 {
		t.Error("normal row column")
	}
	normal.Flags = OpFlagFlipH
	if NormalSrcX(&normal, 3) != 12 {
		t.Error("normal row column flipped")
	}
	scaled := DrawOp{Kind: OpScaledSprite, CharW: 16, CharH: 8}
	scaled.X[0], scaled.X[2] = 100, 107 // 8 destination columns from 16 source columns
	scaled.Y[0], scaled.Y[2] = 0, 3
	want := []int32{1, 3, 5, 7, 9, 11, 13, 15}
	for x, w := range want {
		if got := ScaledSrcX(&scaled, int32(x)); got != w {
			t.Errorf("shrink column %d: %d, want %d", x, got, w)
		}
	}
	scaled.Flags = OpFlagFlipH
	if got := ScaledSrcX(&scaled, 0); got != 14 {
		t.Errorf("shrink column 0 flipped: %d, want 14", got)
	}
	scaled.Flags = OpFlagHSS
	if got := ScaledSrcX(&scaled, 0); got != 0 {
		t.Errorf("HSS even parity: %d, want 0", got)
	}
	scaled.Flags = OpFlagHSS | OpFlagHSSOdd
	if got := ScaledSrcX(&scaled, 1); got != 3 {
		t.Errorf("HSS odd parity: %d, want 3", got)
	}
	enlarge := DrawOp{Kind: OpScaledSprite, CharW: 4, CharH: 8}
	enlarge.X[0], enlarge.X[2] = 0, 7 // 8 destination columns from 4
	wantE := []int32{0, 0, 1, 1, 2, 2, 3, 3}
	for x, w := range wantE {
		if got := ScaledSrcX(&enlarge, int32(x)); got != w {
			t.Errorf("enlarge column %d: %d, want %d", x, got, w)
		}
	}
}

// TestSpriteGouraud checks the bilinear value against the three
// interpolations composed by hand at a corner and in the middle.
func TestSpriteGouraud(t *testing.T) {
	op := DrawOp{Kind: OpNormalSprite, CharW: 8, CharH: 4}
	op.Gouraud = [4]uint32{0x0000, 0x001F, 0x7FFF, 0x7C00}
	if got := SpriteGouraud(&op, 0, 0); got != 0 {
		t.Errorf("corner A: %#x", got)
	}
	if got := SpriteGouraud(&op, 7, 0); got != 0x001F {
		t.Errorf("corner B: %#x", got)
	}
	top := LerpGouraud(0x0000, 0x001F, 3, 8)
	bot := LerpGouraud(0x7C00, 0x7FFF, 3, 8)
	if got, want := SpriteGouraud(&op, 3, 2), LerpGouraud(top, bot, 2, 4); got != want {
		t.Errorf("middle: %#x, want %#x", got, want)
	}
}
