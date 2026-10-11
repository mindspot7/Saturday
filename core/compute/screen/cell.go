// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// LayerCCBit marks a layer buffer pixel as color-calculation enabled,
// the bit the palette word also uses. Priority occupies bits 26:24
// and the decoded color bits 23:0.
const LayerCCBit = 1 << 31

// CellDots is the dots across a cell, the VDP2's character pattern
// unit. A character is one cell or, with the 2x2 size, two across.
// A cell's dots share a pattern name, so it is also the run a cell
// span reads one pattern name for.
const CellDots = 8

// CellDot4 reads one 4-bit dot of a character cell at cellAddr.
func CellDot4(vram *compute.VDP2VRAM, cellAddr uint32, dotX, dotY int32) uint32 {
	b := vram.U8(cellAddr + uint32(dotY*4+dotX/2))
	if dotX&1 == 0 {
		return uint32(b >> 4)
	}
	return uint32(b & 0x0F)
}

// CellDot8 reads one 8-bit dot of a character cell.
func CellDot8(vram *compute.VDP2VRAM, cellAddr uint32, dotX, dotY int32) uint32 {
	return uint32(vram.U8(cellAddr + uint32(dotY*8+dotX)))
}

// CellDot16 reads one 16-bit dot of a character cell.
func CellDot16(vram *compute.VDP2VRAM, cellAddr uint32, dotX, dotY int32) uint32 {
	return uint32(vram.U16BE(cellAddr + uint32((dotY*8+dotX)*2)))
}

// CellDot32 reads one 32-bit RGB dot of a character cell. Returns the
// decoded color and whether the dot is opaque.
func CellDot32(vram *compute.VDP2VRAM, cellAddr uint32, dotX, dotY int32) (rgb uint32, opaque bool) {
	off := cellAddr + uint32((dotY*8+dotX)*4)
	return RGB888Dot(uint32(vram.U16BE(off)), uint32(vram.U16BE(off+2)))
}

// RGB888Dot decodes a 32-bit RGB dot's two words: word 0 holds the
// transparency bit (bit 15, set means opaque per Figure 4.6) and blue
// in bits 7:0, word 1 green in bits 15:8 and red in bits 7:0, so the
// dot's low 24 bits are the decoded color. Returns the decoded color
// and whether the dot is opaque.
func RGB888Dot(w0, w1 uint32) (rgb uint32, opaque bool) {
	return (w0<<16 | w1) & ColorMask, w0&0x8000 != 0
}

// BitmapDot4, BitmapDot8, BitmapDot16, and BitmapDot32 read dot dot
// of the bitmap at base, the dot's index being its row times the
// bitmap's width plus its column, in the 4-bit, 8-bit, 16-bit, and
// 32-bit RGB formats. The bitmap's width is even, so a 4-bit dot's
// nibble follows its column's parity.
func BitmapDot4(vram *compute.VDP2VRAM, base, dot uint32) uint32 {
	b := vram.U8(base + dot/2)
	if dot&1 == 0 {
		return uint32(b >> 4)
	}
	return uint32(b & 0x0F)
}

func BitmapDot8(vram *compute.VDP2VRAM, base, dot uint32) uint32 {
	return uint32(vram.U8(base + dot))
}

func BitmapDot16(vram *compute.VDP2VRAM, base, dot uint32) uint32 {
	return uint32(vram.U16BE(base + dot*2))
}

func BitmapDot32(vram *compute.VDP2VRAM, base, dot uint32) (rgb uint32, opaque bool) {
	off := base + dot*4
	return RGB888Dot(uint32(vram.U16BE(off)), uint32(vram.U16BE(off+2)))
}

// PaletteDot resolves a palette-format dot to its color: dot 0 is
// transparent unless transparency is off; 16-color dots index a
// 16-entry palette, 256-color dots a 256-entry palette selected by
// the palette number's upper bits, wider dots index CRAM directly;
// the screen's CRAM offset adds 256 entries per step.
func PaletteDot(pal *compute.Palette, cramMode uint32, dot, palette, cramOffset, colorMode uint32, transpOff bool) (rgb uint32, transp bool, cc bool) {
	if dot == 0 && !transpOff {
		return 0, true, false
	}
	var addr uint32
	switch colorMode {
	case 0:
		addr = palette*16 + dot
	case 1:
		addr = (palette>>4)*256 + dot
	default:
		addr = dot
	}
	addr += cramOffset * 256
	rgb, cc = PaletteColor(pal, cramMode, addr)
	return rgb, false, cc
}

// DecodePattern1Word extracts the character number, palette, and
// flips from a 1-word pattern name entry under the PNCN supplement
// bits, the color mode, the auxiliary mode, and the character size.
// In 2x2 character mode the character number is shifted left by 2 for
// the sub-cell index and the supplement character bits split around
// it.
func DecodePattern1Word(pn, pncn, colorMode uint32, auxMode1, char2x2 bool) (charNum, palette uint32, hflip, vflip bool) {
	suppPalette := (pncn >> 5) & 0x07
	suppChar := pncn & 0x1F
	if colorMode == 0 {
		palette = (pn>>12)&0x0F | suppPalette<<4
	} else {
		palette = ((pn >> 12) & 0x07) << 4
	}
	if !auxMode1 {
		vflip = pn&0x0800 != 0
		hflip = pn&0x0400 != 0
		if !char2x2 {
			charNum = pn&0x03FF | suppChar<<10
		} else {
			charNum = (pn&0x03FF)<<2 | suppChar&0x03 | (suppChar&0x1C)<<10
		}
	} else {
		if !char2x2 {
			charNum = pn&0x0FFF | (suppChar&0x1C)<<10
		} else {
			charNum = (pn&0x0FFF)<<2 | suppChar&0x03 | (suppChar&0x10)<<10
		}
	}
	return
}

// DecodePattern2Word extracts the fields of a 2-word pattern name
// entry: flips in MSW bits 15 and 14, the special priority and
// special color calculation bits in 13 and 12, the palette in bits
// 6:0, and the character number in LSW bits 14:0.
func DecodePattern2Word(msw, lsw uint32) (charNum, palette uint32, hflip, vflip, specialPri, specialCC bool) {
	return lsw & 0x7FFF, msw & 0x7F, msw&0x4000 != 0, msw&0x8000 != 0, msw&0x2000 != 0, msw&0x1000 != 0
}

// SFCodeMatches reports whether a dot color's low 4 bits fall in a
// nibble pair the special function code enables: bit N of sfcode
// enables dots whose (color & 0xF) >> 1 is N.
func SFCodeMatches(sfcode, dotColor uint32) bool {
	return sfcode&(1<<((dotColor&0x0F)>>1)) != 0
}
