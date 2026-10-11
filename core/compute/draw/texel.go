// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// RowDotNibble, RowDotByte, and RowDotWord read dot x of the
// character row at rowBase in the 4-bit color modes (0 and 1, the
// even dot in the high nibble), the 8-bit modes (2 to 4), and the
// 16-bit mode (5).
func RowDotNibble(vram *compute.VDP1VRAM, rowBase uint32, x int32) uint32 {
	return (uint32(vram.U8(rowBase+uint32(x>>1))) >> (4 - 4*uint32(x&1))) & 0x0F
}

func RowDotByte(vram *compute.VDP1VRAM, rowBase uint32, x int32) uint32 {
	return uint32(vram.U8(rowBase + uint32(x)))
}

func RowDotWord(vram *compute.VDP1VRAM, rowBase uint32, x int32) uint32 {
	return uint32(vram.U16BE(rowBase + uint32(x*2)))
}

// ReadRowDot reads dot x of the character row at rowBase in the
// color mode. The undefined modes 6 and 7 read as zero.
func ReadRowDot(vram *compute.VDP1VRAM, rowBase uint32, x int32, colorMode uint32) uint32 {
	switch colorMode {
	case 0, 1:
		return RowDotNibble(vram, rowBase, x)
	case 2, 3, 4:
		return RowDotByte(vram, rowBase, x)
	case 5:
		return RowDotWord(vram, rowBase, x)
	}
	return 0
}

// IsEndCode reports whether a dot is the color mode's end code: the
// highest dot value of the 4-bit and 8-bit modes, 0x7FFF in the
// 16-bit mode (VDP1 manual Sec 6.3).
func IsEndCode(dot, colorMode uint32) bool {
	switch colorMode {
	case 0, 1:
		return dot == 0xF
	case 2, 3, 4:
		return dot == 0xFF
	case 5:
		return dot == 0x7FFF
	}
	return false
}

// EndCodeLimit is the end codes that end a line: the second one
// (VDP1 manual Sec 6.3).
const EndCodeLimit = 2

// EndCodeCounter is the end code rule's state across a line's dots:
// the end codes counted and, plus one, the source column of the last
// one counted (zero before the first). An end code dot (IsEndCode)
// is never drawn; the counter decides how it counts and when the
// line ends. A shape whose dots read distinct columns passes its
// step as the column, so every end code counts.
type EndCodeCounter struct {
	Count int32
	Last  int32
}

// Reset starts the line with no end codes seen.
func (c *EndCodeCounter) Reset() {
	c.Count = 0
	c.Last = 0
}

// Ends records an end code read from source column column and
// reports whether the line ends: an end code counts once per source
// column, since a shrunk row reads a column more than once, and the
// second one counted ends the line.
func (c *EndCodeCounter) Ends(column int32) bool {
	if column+1 != c.Last {
		c.Last = column + 1
		c.Count++
	}
	return c.Ended()
}

// Ended reports whether the end codes counted have ended the line.
func (c *EndCodeCounter) Ended() bool {
	return c.Count >= EndCodeLimit
}

// EndsOnCount reports whether the end code counter cuts a line of an
// op drawn with the pixel params: only at native. A scaled line
// samples its character more finely than the native line, reading
// columns the native line skipped or a column more times, so a fresh
// count would end it early; at scale an end code dot is still skipped
// and counted, and the native line's recorded end alone cuts the
// line.
func EndsOnCount(p *PixelParams) bool {
	return p.ScaleShift == 0
}

// TexelDraws applies the texel rules to a dot read from source
// column column and reports whether the dot is drawn. An end code,
// when the line checks them, is not drawn and counts through the
// counter, whose Ended then says whether the line ends; a zero dot
// is transparent and not drawn unless SPD draws it (CMDPMOD SPD),
// spd being the command's OpFlagSPDOn bit, nonzero when it does.
func TexelDraws(dot, colorMode, spd uint32, checkECD bool, ec *EndCodeCounter, column int32) bool {
	if checkECD && IsEndCode(dot, colorMode) {
		ec.Ends(column)
		return false
	}
	return dot|spd != 0
}

// sourceAdvance is the source read a textured step charges: the
// distance from the last source column read to this one, halved
// under high speed shrink, at least one, and one on a line's first
// read (prev -1).
func sourceAdvance(prev, src int32, hssShrink bool) int32 {
	adv := int32(1)
	if prev >= 0 {
		da := absI32(src - prev)
		if hssShrink {
			da = (da + 1) / 2
		}
		if da > 1 {
			adv = da
		}
	}
	return adv
}

// stepCycles is the cycles a textured step charges: the source
// advance when the dot is skipped, the larger of the advance and
// pixCycles when it is drawn. A normal sprite's advance is one.
func stepCycles(adv, pixCycles int32, drawn bool) int32 {
	cycles := adv
	if drawn && pixCycles > adv {
		cycles = pixCycles
	}
	return cycles
}

// dotColorMask is, per color mode, the CMDCOLR bits a dot's pixel
// keeps in the modes that combine CMDCOLR with the dot: 4 dot bits in
// mode 0, 6 in mode 2, 7 in mode 3, 8 in mode 4.
var dotColorMask = [8]uint32{0xFFF0, 0, 0xFFC0, 0xFF80, 0xFF00, 0, 0, 0}

// DotToPixel converts a dot to a frame buffer pixel: mode 1 looks the
// dot up in the color lookup table at CMDCOLR, mode 5 is the dot
// itself, the other defined modes combine CMDCOLR's upper bits with
// the dot, and the undefined modes 6 and 7 give zero.
func DotToPixel(vram *compute.VDP1VRAM, dot, cmdcolr, colorMode uint32) uint32 {
	if colorMode == 1 {
		return uint32(vram.U16BE(cmdcolr*8 + dot*2))
	}
	if colorMode == 5 {
		return dot
	}
	m := dotColorMask[colorMode&7]
	return (cmdcolr & m) | (dot & (0xFFFF ^ m))
}
