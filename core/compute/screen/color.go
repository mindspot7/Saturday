// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

// A decoded color is the VDP2's 24-bit color data layout (VDP2 manual
// Sec 3.4 Figure 3.9 and Sec 4 Figure 4.6): bits 23:16 blue, 15:8
// green, and 7:0 red. The bits above 23 are free for the flags of the
// word the color is packed into.

// ColorMask selects a decoded color's channel bits.
const ColorMask = 0xFFFFFF

// RGB555To888 expands 15-bit color data (bit 15 ignored, bits 14:10
// blue, 9:5 green, 4:0 red) to a decoded color. Each channel's 5 bits
// become the upper 5 bits of its 8 and the lower 3 bits are 0 (VDP2
// manual Sec 3.4, "a 0 will be added to the lowest 3 bits"; Sec 4 RGB
// Format Dot Data; Figure 7.5).
func RGB555To888(val uint32) uint32 {
	return PackColor((val&0x1F)<<3, (val>>5&0x1F)<<3, (val>>10&0x1F)<<3)
}

// ColorR, ColorG, and ColorB extract a decoded color's channels.
func ColorR(c uint32) uint32 { return c & 0xFF }
func ColorG(c uint32) uint32 { return c >> 8 & 0xFF }
func ColorB(c uint32) uint32 { return c >> 16 & 0xFF }

// PackColor packs 8-bit channels into a decoded color.
func PackColor(r, g, b uint32) uint32 {
	return b<<16 | g<<8 | r
}
