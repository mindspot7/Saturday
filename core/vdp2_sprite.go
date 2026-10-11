// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/user-none/erings/core/compute/screen"

// spriteRow describes the VDP1 frame buffer read for field-line y
// (screen.SpriteRow): the direct 16-bit or 8-bit row read, with
// hi-res halving the column for a 16-bit buffer, or the rotated read
// from rotation parameter A. The caller passes a field-line y in
// [0, activeLines); the frame buffer holds one field's rows, so no Y
// halving applies.
func (v *VDP2) spriteRow(y int) screen.SpriteRow {
	fb := &v.lineFB
	r := screen.SpriteRow{Width: int32(fb.width), Height: int32(fb.height)}
	switch {
	case fb.data == nil || y >= fb.height:
	case v.lineSprRot.enabled:
		r.Mode = screen.SpriteReadRotated
		r.BaseX, r.BaseY = v.lineSprRot.baseX, v.lineSprRot.baseY
		r.DX, r.DY = v.lineSprRot.dx, v.lineSprRot.dy
		r.Is8bpp = fb.is8bpp
	case fb.is8bpp:
		r.Mode = screen.SpriteRead8
		r.Row = uint32(y * fb.width)
	default:
		r.Mode = screen.SpriteRead16
		r.Row = uint32(y*fb.width) * 2
		if v.frame.hiRes {
			r.Halve = 1
		}
	}
	return r
}

// spriteRegs gathers the sprite registers from the line snapshot for
// the render sprite decode.
func (v *VDP2) spriteRegs() screen.SpriteRegs {
	regs := &v.frame.regs
	return screen.SpriteRegs{
		SPCTL:  uint32(regs[vdp2SPCTL]),
		CRAOFB: uint32(regs[vdp2CRAOFB]),
		PRIS:   [4]uint32{uint32(regs[vdp2PRISA]), uint32(regs[vdp2PRISB]), uint32(regs[vdp2PRISC]), uint32(regs[vdp2PRISD])},
		CCRS:   [4]uint32{uint32(regs[vdp2CCRSA]), uint32(regs[vdp2CCRSB]), uint32(regs[vdp2CCRSC]), uint32(regs[vdp2CCRSD])},
		CCCTL:  uint32(regs[vdp2CCCTL]),
		SDCTL:  uint32(regs[vdp2SDCTL]),
	}
}

// buildSpriteDecode resolves the row's sprite decode from the line
// register snapshot.
func (v *VDP2) buildSpriteDecode() screen.SpriteDecode {
	return screen.BuildSpriteDecode(v.spriteRegs())
}

// isCCEnabled returns true if color calculation is enabled for the given layer.
func isCCEnabled(ccctl uint16, layerID int) bool {
	switch layerID {
	case 5: // sprite -> CCCTL bit 6
		return ccctl&(1<<6) != 0
	case 4: // RBG0 -> CCCTL bit 4
		return ccctl&(1<<4) != 0
	default: // NBG0-3 -> CCCTL bits 0-3
		return ccctl&(1<<uint(layerID)) != 0
	}
}
