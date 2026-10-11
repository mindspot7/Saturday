// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestPaletteEntry verifies the entry decode per CRAM mode: 15-bit
// color with the CC bit in modes 0 and 1 (1024 and 2048 entries),
// 24-bit color across two words with the CC bit in the upper word in
// modes 2 and 3. The palette word keeps the hardware channel layout.
func TestPaletteEntry(t *testing.T) {
	cram := new(compute.CRAM)
	cram[5*2], cram[5*2+1] = 0x80, 0x1F       // entry 5: red, CC set
	cram[1500*2], cram[1500*2+1] = 0x03, 0xE0 // entry 1500: green (mode 1 only)
	cram.PutU32BE(7*4, 0x81AA<<16|0xBBCC)     // mode 2 entry 7: B=AA, G=BB, R=CC, CC set, ignored bit 24

	if e := PaletteEntry(cram, 0, 5); e != 0x80000000|0x0000F8 {
		t.Errorf("mode 0 entry 5 = 0x%08X", e)
	}
	if e := PaletteEntry(cram, 0, 1500); e != PaletteEntry(cram, 0, 1500&1023) {
		t.Errorf("mode 0 masks to 1024 entries")
	}
	if e := PaletteEntry(cram, 1, 1500); e != 0x00F800 {
		t.Errorf("mode 1 entry 1500 = 0x%08X, want green", e)
	}
	if e := PaletteEntry(cram, 2, 7); e != 0x80AABBCC {
		t.Errorf("mode 2 entry 7 = 0x%08X, want 0x80AABBCC", e)
	}
	pal := new(compute.Palette)
	BuildPalette(cram, pal, &PaletteParams{Mode: 0}, 5)
	if rgb, cc := PaletteColor(pal, 0, 5); rgb != 0x0000F8 || !cc {
		t.Errorf("PaletteColor = 0x%06X %v", rgb, cc)
	}
	if rgb, _ := PaletteColor(pal, 0, 5+1024); rgb != 0x0000F8 {
		t.Error("PaletteColor does not mask by mode")
	}
}

// TestRGB555To888 verifies the 15-bit expansion into the decoded
// color layout: blue bits 23:16, green 15:8, red 7:0. Each 5-bit
// channel is shifted left 3 with the low 3 bits 0.
func TestRGB555To888(t *testing.T) {
	if got := RGB555To888(0x7FFF); got != 0xF8F8F8 {
		t.Errorf("white = 0x%06X", got)
	}
	if got := RGB555To888(0x0001); got != 0x000008 {
		t.Errorf("red 1 = 0x%06X, want 0x000008", got)
	}
	if got := RGB555To888(1 << 5); got != 0x000800 {
		t.Errorf("green 1 = 0x%06X, want 0x000800", got)
	}
	if got := RGB555To888(0x8000 | 1<<10); got != 0x080000 {
		t.Errorf("blue 1 ignores bit 15 = 0x%06X", got)
	}
}

// TestColorChannels verifies PackColor and the channel extractors
// agree on the decoded color layout.
func TestColorChannels(t *testing.T) {
	c := PackColor(0x12, 0x34, 0x56)
	if c != 0x563412 {
		t.Errorf("PackColor = 0x%06X, want 0x563412", c)
	}
	if r, g, b := ColorR(c), ColorG(c), ColorB(c); r != 0x12 || g != 0x34 || b != 0x56 {
		t.Errorf("channels = %02X %02X %02X", r, g, b)
	}
	if r, g, b := ColorR(0xFF000000|c), ColorG(0xFF000000|c), ColorB(0xFF000000|c); r != 0x12 || g != 0x34 || b != 0x56 {
		t.Errorf("channels with flag bits = %02X %02X %02X", r, g, b)
	}
}

// TestRGB888Dot verifies a 32-bit RGB dot decodes to its low 24 bits
// with the transparency bit from word 0 bit 15.
func TestRGB888Dot(t *testing.T) {
	rgb, opaque := RGB888Dot(0x81AA, 0xBBCC)
	if rgb != 0xAABBCC || !opaque {
		t.Errorf("RGB888Dot = 0x%06X %v, want 0xAABBCC true", rgb, opaque)
	}
	if _, opaque := RGB888Dot(0x00AA, 0xBBCC); opaque {
		t.Error("bit 15 clear should be transparent")
	}
}
