// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// CopyParams locates a band of staged pages: the slot of its first
// page in the page store and the page table. Slot s of the band holds
// the bytes of page table entry Base+s, and the copy writes them to
// that page of the memory. Applied in band order, the pages reproduce
// the memory as of the band's line.
type CopyParams struct {
	Base uint32
}

// VRAMPages is a submission's staged VDP2 VRAM pages: the page store,
// a whole page per slot in memory order, and the page table, the
// destination page number of each slot.
type VRAMPages struct {
	Store *compute.PageStore
	Table *compute.PageTable
}

// CRAMPages is a submission's staged CRAM pages, in the form of
// VRAMPages.
type CRAMPages struct {
	Store *compute.CRAMPageStore
	Table *compute.PageTable
}

// CopyVRAMPages writes word i of a band's staged pages into VDP2
// VRAM. Entry point: one thread per word.
func CopyVRAMPages(vram *compute.VDP2VRAM, pages *VRAMPages, p *CopyParams, i uint32) {
	slot := p.Base + i/compute.PageWords
	word := i % compute.PageWords
	src := slot*compute.PageBytes + word*4
	dst := pages.Table[slot]*compute.PageBytes + word*4
	vram.PutU32BE(dst, pages.Store.U32BE(src))
}

// CopyCRAMPages writes word i of a band's staged pages into VDP2
// CRAM. Entry point: one thread per word.
func CopyCRAMPages(cram *compute.CRAM, pages *CRAMPages, p *CopyParams, i uint32) {
	slot := p.Base + i/compute.PageWords
	word := i % compute.PageWords
	src := slot*compute.PageBytes + word*4
	dst := pages.Table[slot]*compute.PageBytes + word*4
	cram.PutU32BE(dst, pages.Store.U32BE(src))
}
