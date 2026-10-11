// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute/draw"
)

// clipBounds returns the effective system clip bounds clamped to the
// frame buffer dimensions for the current mode. Under double interlace
// (DIE=1) the Y range doubles; the pixel write then halves surviving Y
// values to the physical FB row. The bounds are never negative, which
// is what lets the pixel write's clip test compare unsigned: the
// clipping command decodes them from unsigned fields (clipCoordX,
// clipCoordY), and a state file is the one other source.
func (v *VDP1) clipBounds() (int, int) {
	clipX := int(v.sysClipX)
	clipY := int(v.sysClipY)
	maxX := v.fbWidth() - 1
	maxY := v.fbHeightLogical() - 1
	if clipX > maxX {
		clipX = maxX
	}
	if clipY > maxY {
		clipY = maxY
	}
	if clipX < 0 {
		clipX = 0
	}
	if clipY < 0 {
		clipY = 0
	}
	return clipX, clipY
}

// readGouraudTable reads 4 Gouraud correction entries from VRAM.
// Returns raw packed values (bits 14:10=B, 9:5=G, 4:0=R).
func (v *VDP1) readGouraudTable(grda uint16) [4]uint16 {
	addr := uint32(grda) * 8
	vram := v.vram
	var t [4]uint16
	for i := 0; i < 4; i++ {
		a := (addr + uint32(i)*2) & (vdp1VRAMSize - 1)
		t[i] = uint16(vram[a])<<8 | uint16(vram[(a+1)&(vdp1VRAMSize-1)])
	}
	return t
}

// pixelParams resolves the per-command pixel write parameters
// (draw.PixelParams) from the command's mode bits, the clip
// state, and the frame buffer mode. An 8-bit frame buffer and MSB-on
// both force replace mode. The user clip rides as exclusive bounds:
// mode 2 draws strictly inside the rectangle, and mode 3 draws
// strictly outside it with the rectangle's edges included in the
// rejection, which is strictly outside the rectangle widened by one,
// so the pixel write tests one strict interior for both modes.
func (v *VDP1) pixelParams(cc uint16, msbOn, mesh bool, userClip uint16, clipX, clipY int) draw.PixelParams {
	if v.is8bpp() {
		cc = 0
		msbOn = false
	}
	if msbOn {
		cc = 0
	}
	widen := int32(0)
	if userClip == 3 {
		widen = 1
	}
	p := draw.PixelParams{
		CC:       uint32(cc),
		UserClip: uint32(userClip),
		UCX1:     int32(v.userClipX1) - widen,
		UCY1:     int32(v.userClipY1) - widen,
		UCX2:     int32(v.userClipX2) + widen,
		UCY2:     int32(v.userClipY2) + widen,
		ClipX:    int32(clipX),
		ClipY:    int32(clipY),
		FBWidth:  int32(v.fbWidth()),
		FBHeight: int32(v.fbHeight()),
	}
	if msbOn {
		p.MSBOn = 1
	}
	if mesh {
		p.Mesh = 1
	}
	if v.dieEnabled() {
		p.DIE = 1
	}
	if v.dilOdd() {
		p.DILOdd = 1
	}
	p.Is8bpp = v.is8bpp()
	return p
}

// vdp1Command holds a parsed VDP1 command table entry.
type vdp1Command struct {
	ctrl uint16
	link uint16
	pmod uint16
	colr uint16
	srca uint16
	size uint16
	xa   int16
	ya   int16
	xb   int16
	yb   int16
	xc   int16
	yc   int16
	xd   int16
	yd   int16
	grda uint16
}

// readVDP1VRAM16 reads a big-endian 16-bit value from VDP1 VRAM.
func (v *VDP1) readVDP1VRAM16(addr uint32) uint16 {
	addr &= vdp1VRAMSize - 1
	hi := v.vram[addr]
	lo := v.vram[(addr+1)&(vdp1VRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

// readCommand reads a command table entry from VRAM at the given byte address.
func (v *VDP1) readCommand(addr uint32) vdp1Command {
	return vdp1Command{
		ctrl: v.readVDP1VRAM16(addr + 0x00),
		link: v.readVDP1VRAM16(addr + 0x02),
		pmod: v.readVDP1VRAM16(addr + 0x04),
		colr: v.readVDP1VRAM16(addr + 0x06),
		srca: v.readVDP1VRAM16(addr + 0x08),
		size: v.readVDP1VRAM16(addr + 0x0A),
		xa:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x0C)),
		ya:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x0E)),
		xb:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x10)),
		yb:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x12)),
		xc:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x14)),
		yc:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x16)),
		xd:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x18)),
		yd:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x1A)),
		grda: v.readVDP1VRAM16(addr + 0x1C),
	}
}

// signExtendCoord13 decodes a CMDXA-CMDYD coordinate field. The
// hardware-effective field is 13 bits (bits 12..0) with the sign at
// bit 12; bits 15..13 are reserved code-extension and ignored. This
// matches games that submit coordinates outside the 11-bit range
// quoted by the manual - without bit 11/12 included, large positive
// values get misread as negatives and degenerate quads spanning the
// visible area get rasterized instead of pre-clip-rejected.
func signExtendCoord13(v uint16) int16 {
	v &= 0x1FFF
	if v&0x1000 != 0 {
		v |= 0xE000
	}
	return int16(v)
}

// clipCoordX and clipCoordY narrow a coordinate of the system or user
// clipping coordinate set command to its field. Per the VDP1 User's
// Manual sections 7.1 and 7.2, the two clipping command tables define
// CMDXA and CMDXC as a 10-bit X coordinate and CMDYA and CMDYC as a
// 9-bit Y coordinate, the bits above each field being ignored, so a
// clipping coordinate is unsigned and the pixel write's clip compares
// never see a negative bound. Section 7.1 states operation cannot be
// ensured for a negative system clipping coordinate.
func clipCoordX(v int16) int16 { return v & 0x3FF }

func clipCoordY(v int16) int16 { return v & 0x1FF }
