// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

import "testing"

// TestByteBufferAccess verifies the big-endian accessors: a write
// reads back at every width, an address past the end wraps, and an
// access straddling the end wraps its later bytes, on VDP2 VRAM,
// which has every accessor, and on the other buffers' accessors.
func TestByteBufferAccess(t *testing.T) {
	vram := new(VDP2VRAM)
	vram.PutU32BE(0x100, 0x11223344)
	if got := vram.U8(0x100); got != 0x11 {
		t.Errorf("U8 = 0x%02X, want 0x11", got)
	}
	if got := vram.U16BE(0x101); got != 0x2233 {
		t.Errorf("U16BE = 0x%04X, want 0x2233", got)
	}
	if got := vram.U32BE(0x100); got != 0x11223344 {
		t.Errorf("U32BE = 0x%08X, want 0x11223344", got)
	}
	vram.PutU16BE(VDP2VRAMSize-1, 0xABCD)
	if got := vram.U8(VDP2VRAMSize - 1); got != 0xAB {
		t.Errorf("last byte = 0x%02X, want 0xAB", got)
	}
	if got := vram.U8(0); got != 0xCD {
		t.Errorf("wrapped byte = 0x%02X, want 0xCD", got)
	}
	if got := vram.U16BE(VDP2VRAMSize + 0x100); got != 0x1122 {
		t.Errorf("wrapped address U16BE = 0x%04X, want 0x1122", got)
	}
	vram.PutU8(VDP2VRAMSize*2+7, 0x5A)
	if got := vram.U8(7); got != 0x5A {
		t.Errorf("wrapped PutU8 = 0x%02X, want 0x5A", got)
	}

	cram := new(CRAM)
	cram.PutU32BE(CRAMSize-2, 0x7FFF1234)
	if got := cram.U16BE(CRAMSize - 2); got != 0x7FFF {
		t.Errorf("CRAM last word = 0x%04X, want 0x7FFF", got)
	}
	if got := cram.U16BE(0); got != 0x1234 {
		t.Errorf("CRAM straddling write = 0x%04X, want 0x1234", got)
	}
	v1 := new(VDP1VRAM)
	v1.PutU32BE(VDP1VRAMSize*3+4, 0x0102A5B6)
	if got := v1.U16BE(6); got != 0xA5B6 {
		t.Errorf("VDP1VRAM wrapped write = 0x%04X, want 0xA5B6", got)
	}
	if got := v1.U8(4); got != 0x01 {
		t.Errorf("VDP1VRAM U8 = 0x%02X, want 0x01", got)
	}
	fb := make(VDP1FB, VDP1FBSize)
	fb.PutU16BE(VDP1FBSize-1, 0x0304)
	if got := fb.U8(0); got != 0x04 {
		t.Errorf("VDP1FB straddling write = 0x%02X, want 0x04", got)
	}
	fb.PutU8(VDP1FBSize+9, 0x77)
	if got := fb.U16BE(8); got != 0x0077 {
		t.Errorf("VDP1FB wrapped PutU8 = 0x%04X, want 0x0077", got)
	}
	const lcBytes = 1 << 12
	lc := make(LineColor, lcBytes)
	lc.PutU8(lcBytes+3, 0x85)
	if got := lc.U8(3); got != 0x85 {
		t.Errorf("LineColor wrapped PutU8 = 0x%02X, want 0x85", got)
	}
	store := new(VDP1PageStore)
	store[VDP1PageStoreBytes-1], store[0], store[1], store[2] = 1, 2, 3, 4
	if got := store.U32BE(VDP1PageStoreBytes - 1); got != 0x01020304 {
		t.Errorf("VDP1PageStore straddling read = 0x%08X, want 0x01020304", got)
	}
	if got := store.U8(VDP1PageStoreBytes); got != 2 {
		t.Errorf("VDP1PageStore wrapped U8 = %d, want 2", got)
	}
}

// TestSliceConversion verifies a renderer slice of the exact length
// converts to the buffer pointer and shares storage.
func TestSliceConversion(t *testing.T) {
	s := make([]byte, CRAMSize)
	b := (*CRAM)(s)
	b.PutU32BE(10, 0x12345678)
	if s[10] != 0x12 || s[13] != 0x78 {
		t.Errorf("slice bytes = %02X %02X, want 12 78", s[10], s[13])
	}
	w := make([]uint32, PaletteWords)
	l := (*Palette)(w)
	l[3] = 0xDEADBEEF
	if w[3] != 0xDEADBEEF {
		t.Errorf("word store did not reach the slice")
	}
}

// TestSharedWordHelpers verifies the software bodies of the round
// state's shared-word operations: Raise keeps the greatest value,
// Swap and Add return the prior value, and each masks its index with
// the region's power-of-two length.
func TestSharedWordHelpers(t *testing.T) {
	stamps := make(OrderStamps, 8)
	stamps.Raise(2, 5)
	stamps.Raise(2, 3)
	stamps.Raise(10, 9)
	if stamps[2] != 9 {
		t.Errorf("Raise = %d, want 9", stamps[2])
	}
	heads := make(FragmentHeads, 8)
	if prior := heads.Swap(1, 4); prior != 0 {
		t.Errorf("first Swap prior = %d, want 0", prior)
	}
	if prior := heads.Swap(9, 6); prior != 4 || heads[1] != 6 {
		t.Errorf("second Swap prior = %d, word %d, want 4 and 6", prior, heads[1])
	}
	pool := make(FragmentPool, 8)
	if prior := pool.Add(0, 4); prior != 0 {
		t.Errorf("first Add prior = %d, want 0", prior)
	}
	if prior := pool.Add(8, 4); prior != 4 || pool[0] != 8 {
		t.Errorf("second Add prior = %d, word %d, want 4 and 8", prior, pool[0])
	}
}
