// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

// readVRAM16 reads a big-endian 16-bit value from VDP2 VRAM for the
// row setup's table reads.
func (v *VDP2) readVRAM16(addr uint32) uint16 {
	vram := v.vram
	addr &= vdp2VRAMSize - 1
	hi := vram[addr]
	lo := vram[(addr+1)&(vdp2VRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

// buildCRAMCache decodes every CRAM entry for the current CRAM mode
// into the palette (screen.BuildPalette per entry) so the
// per-pixel color path becomes one word load. Called from BeginLine,
// it early-returns while cramCacheValid is set and rebuilds only after
// a CRAM write or a CRAM-mode (RAMCTL) change has cleared the flag, so
// it rebuilds lazily on a later line rather than once per frame or
// every line.
func (v *VDP2) buildCRAMCache() {
	if v.cramCacheValid {
		return
	}
	mode := uint32(v.frame.cramMode)
	p := screen.PaletteParams{Mode: mode}
	cram := (*compute.CRAM)(v.cram)
	v.cramCacheMode = mode
	v.cramCacheMask = screen.PaletteMask(mode)
	for i := uint32(0); i <= v.cramCacheMask; i++ {
		screen.BuildPalette(cram, &v.palette, &p, i)
	}
	v.cramCacheValid = true
}

// updatePaletteEntry re-decodes the palette entry holding CRAM byte
// addr for the mode the palette was built for: two bytes per entry in
// modes 0 and 1, four in modes 2 and 3. In mode 0 the mirrored halves
// map to the same entry index.
func (v *VDP2) updatePaletteEntry(addr uint32) {
	i := addr / 2
	if v.cramCacheMode >= 2 {
		i = addr / 4
	}
	p := screen.PaletteParams{Mode: v.cramCacheMode}
	screen.BuildPalette((*compute.CRAM)(v.cram), &v.palette, &p, i)
}
