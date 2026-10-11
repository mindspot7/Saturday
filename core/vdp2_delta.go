// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"math/bits"
	"sync/atomic"

	"github.com/user-none/erings/core/compute"
)

// Page delivery
//
// The GPU renderer renders a row after the walker has moved on, so
// it needs VRAM and CRAM as the row read them. Both are tracked in
// pages of compute.PageBytes: every write marks its page,
// and the renderer takes the marked pages at a row's first render
// point, the point the software renderer starts reading the row's
// memory, copying each into a staging store with its page number,
// stamped with the row. The device applies those pages before the
// row. Marking is always on: a plain load of the page's bit and an
// atomic OR only when the bit is clear, so a burst of writes to one
// page costs one atomic. The words are written under the bus lock by
// the CPUs and the SCU DMA and swapped to zero by the walker, so a
// write landing during a take stays marked for the next take. A take
// copies live memory while writers may still be storing into it: a
// page torn that way is marked again by the write that tore it and
// copied whole at the next take.

// vdp2VRAMPages and vdp2CRAMPages are the pages of each memory.
const (
	vdp2VRAMPages = vdp2VRAMSize / compute.PageBytes
	vdp2CRAMPages = vdp2CRAMSize / compute.PageBytes
)

// pageDirty is the dirty map of one memory, one bit per page. It is
// sized for VRAM; CRAM uses the low bits of the first word.
type pageDirty struct {
	words [vdp2VRAMPages / 64]atomic.Uint64
}

// mark sets page's bit.
func (d *pageDirty) mark(page uint32) {
	w := &d.words[page>>6]
	bit := uint64(1) << (page & 63)
	if w.Load()&bit == 0 {
		w.Or(bit)
	}
}

// markAll sets the bits of the memory's pages pages.
func (d *pageDirty) markAll(pages int) {
	for i := range d.words {
		n := pages - i*64
		switch {
		case n >= 64:
			d.words[i].Store(^uint64(0))
		case n > 0:
			d.words[i].Store(1<<uint(n) - 1)
		default:
			d.words[i].Store(0)
		}
	}
}

// take clears every bit and appends the pages that were set to dst
// in ascending order.
func (d *pageDirty) take(dst []uint32) []uint32 {
	for i := range d.words {
		w := d.words[i].Swap(0)
		for w != 0 {
			b := bits.TrailingZeros64(w)
			dst = append(dst, uint32(i*64+b))
			w &^= 1 << uint(b)
		}
	}
	return dst
}

// markVRAMPage marks the VRAM page holding byte addr. A write is
// aligned to its width and a page is a multiple of every width, so a
// write never straddles pages.
func (v *VDP2) markVRAMPage(addr uint32) { v.vramDirty.mark(addr / compute.PageBytes) }

// markCRAMPage marks the CRAM page holding byte addr.
func (v *VDP2) markCRAMPage(addr uint32) { v.cramDirty.mark(addr / compute.PageBytes) }

// markAllPages marks every page of VRAM and CRAM: their contents were
// replaced outside the write funnel (reset, save state load).
func (v *VDP2) markAllPages() {
	v.vramDirty.markAll(vdp2VRAMPages)
	v.cramDirty.markAll(vdp2CRAMPages)
}
