// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

// Buffer sizes. The renderers size their intermediate buffers (the
// layer, line color, and output frame buffers) themselves, each a
// power of two so masked access applies to them as well.
const (
	VDP2VRAMSize = 512 * 1024 // VDP2 VRAM, bytes
	CRAMSize     = 4 * 1024   // VDP2 color RAM, bytes
	VDP1VRAMSize = 512 * 1024 // VDP1 VRAM, bytes
	VDP1FBSize   = 256 * 1024 // one VDP1 frame buffer, bytes

	// PaletteWords holds one decoded entry per CRAM color (2048 in
	// CRAM mode 1, 1024 otherwise).
	PaletteWords = 2048

	// PageBytes is the unit VDP2 VRAM and CRAM are delivered to a
	// device in: a renderer stages whole pages of the live memory in
	// a page store with the destination page number of each in a
	// page table, and a copy entry writes them into the device
	// memory. PageWords is the words in a page, the copy entry's
	// threads per page.
	PageBytes = 1024
	PageWords = PageBytes / 4
	// PageStoreBytes holds the VDP2 VRAM pages staged for one
	// submission, CRAMPageStoreBytes the CRAM pages, and
	// PageTableWords the page numbers of either store.
	PageStoreBytes     = 4096 * PageBytes
	CRAMPageStoreBytes = 1024 * PageBytes
	PageTableWords     = 4096
	// VDP1PageStoreBytes holds the VDP1 VRAM pages staged for one
	// submission, eight times the VRAM, so a take that must still fit
	// the whole VRAM rarely forces a submission.
	VDP1PageStoreBytes = 8 * VDP1VRAMSize
)

// Fixed-size buffers are arrays, which compute functions take by
// pointer; the renderer converts a slice of the exact length with a
// slice-to-array-pointer conversion. Variable-length buffers are
// byte or word slices, which compute functions take by value. Word
// buffers are indexed; byte buffers are read and written through the
// accessors in software.go.

// VDP2VRAM is the VDP2 VRAM.
type VDP2VRAM [VDP2VRAMSize]byte

// CRAM is the VDP2 color RAM.
type CRAM [CRAMSize]byte

// VDP1VRAM is the VDP1 VRAM.
type VDP1VRAM [VDP1VRAMSize]byte

// Palette is the CRAM decoded per entry to a packed color: bit 31 the
// entry's color calculation bit, bits 23:0 the decoded color (bits
// 23:16 blue, 15:8 green, 7:0 red).
type Palette [PaletteWords]uint32

// PageStore holds staged VDP2 VRAM pages, CRAMPageStore staged CRAM
// pages, each a whole page per slot in memory order.
type PageStore [PageStoreBytes]byte
type CRAMPageStore [CRAMPageStoreBytes]byte

// VDP1PageStore holds staged VDP1 VRAM pages, or staged pages of the
// bytes the CPU wrote into a VDP1 frame buffer.
type VDP1PageStore [VDP1PageStoreBytes]byte

// MaskStoreWords is the words of a MaskStore: one bit per byte of
// every slot of a VDP1PageStore.
const MaskStoreWords = VDP1PageStoreBytes / 32

// MaskStore holds, per staged page of CPU frame buffer writes, the
// bits of the bytes the CPU wrote.
type MaskStore [MaskStoreWords]uint32

// PageTable holds the destination page number of each staged slot.
type PageTable [PageTableWords]uint32

// LineRecords holds host-generated VDP1 line records. The host owns
// its capacity; compute functions receive a view and cannot resize it.
//
// The length of every variable-length buffer is a power of two,
// checked where the host allocates it and where a backend binds it.
// An index past the length is a host error: Go stops on it, and a
// backend masks the index with the length, so the access stays
// inside the buffer.
type LineRecords []uint32

// Layer is one layer buffer of packed pixels, the rows the host has
// in flight, each the row's width long.
type Layer []uint32

// Framebuffer is the output frame buffer, one word per pixel of the
// frame: the decoded color (bits 23:16 blue, 15:8 green, 7:0 red) with
// bits 31:24 set, so a pixel's bytes in little-endian host memory are
// R, G, B, and an opaque alpha.
type Framebuffer []uint32

// LineColor holds a rotation screen's per-dot coefficient line color
// data for the rows the host has in flight: one byte per dot, bit 7
// set when the dot's coefficient supplied one, bits 6:0 the data.
type LineColor []byte

// VDP1FB is one VDP1 frame buffer in the hardware layout, VDP1FBSize
// long at the native resolution: 16-bit big-endian pixels, or 8-bit
// pixels in the 8-bit modes.
type VDP1FB []byte
