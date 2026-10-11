// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// PaletteParams carries the CRAM mode (RAMCTL bits 13:12) for a
// palette build.
type PaletteParams struct {
	Mode uint32
}

// PaletteMask returns the entry index mask for a CRAM mode: 2048
// entries in mode 1, 1024 otherwise.
func PaletteMask(mode uint32) uint32 {
	if mode == 1 {
		return 2047
	}
	return 1023
}

// PaletteEntry decodes CRAM entry i for the mode into a palette word:
// bit 31 the color calculation bit and bits 23:0 the decoded color
// (Figure 3.9). In modes 0 and 1 an entry is 15-bit color data with
// the color calculation bit in bit 15; in modes 2 and 3 it is 24-bit
// color data with the bit in bit 31, the same layout as the palette
// word.
func PaletteEntry(cram *compute.CRAM, mode uint32, i uint32) uint32 {
	i &= PaletteMask(mode)
	if mode <= 1 {
		val := uint32(cram.U16BE(i * 2))
		return val&0x8000<<16 | RGB555To888(val)
	}
	val := uint32(cram.U16BE(i*4))<<16 | uint32(cram.U16BE(i*4+2))
	return val & (0x80000000 | ColorMask)
}

// BuildPalette writes palette entry i. Entry point: one thread per
// entry.
func BuildPalette(cram *compute.CRAM, pal *compute.Palette, p *PaletteParams, i uint32) {
	pal[i] = PaletteEntry(cram, p.Mode, i)
}

// PaletteColor reads the color for a CRAM address in the mode: the
// decoded color and the color calculation bit.
func PaletteColor(pal *compute.Palette, mode uint32, addr uint32) (rgb uint32, cc bool) {
	e := pal[addr&PaletteMask(mode)]
	return e & ColorMask, e&0x80000000 != 0
}
