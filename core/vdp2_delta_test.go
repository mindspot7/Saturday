// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"slices"
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestPageDirtyMarks: a write of each width marks the page it lands
// in and no other, a take returns the marked pages in ascending order
// and clears them, a CRAM write in mode 0 marks the mirror's page as
// well, in mode 1 only its own, and markAll marks exactly the
// memory's pages.
func TestPageDirtyMarks(t *testing.T) {
	v := NewVDP2(NewSCU())
	pages := make([]uint32, 0, vdp2VRAMPages)
	v.vramDirty.take(pages)
	v.cramDirty.take(pages)

	v.WriteVRAM(compute.PageBytes-1, 1)
	v.WriteVRAM16(compute.PageBytes, 2)
	v.WriteVRAM32(vdp2VRAMSize-4, 3)
	if got := v.vramDirty.take(pages[:0]); !slices.Equal(got, []uint32{0, 1, vdp2VRAMPages - 1}) {
		t.Fatalf("VRAM pages taken = %v", got)
	}
	if got := v.vramDirty.take(pages[:0]); len(got) != 0 {
		t.Fatalf("a take left pages marked: %v", got)
	}

	v.regs[vdp2RAMCTL] = 0
	v.WriteCRAM16(0x10, 5)
	if got := v.cramDirty.take(pages[:0]); !slices.Equal(got, []uint32{0, 2}) {
		t.Fatalf("CRAM pages taken in mode 0 = %v, want the page and its mirror", got)
	}
	v.regs[vdp2RAMCTL] = 1 << 12
	v.WriteCRAM(3*compute.PageBytes, 1)
	if got := v.cramDirty.take(pages[:0]); !slices.Equal(got, []uint32{3}) {
		t.Fatalf("CRAM pages taken in mode 1 = %v", got)
	}

	v.markAllPages()
	got := v.vramDirty.take(pages[:0])
	if len(got) != vdp2VRAMPages {
		t.Fatalf("markAll marked %d VRAM pages, want %d", len(got), vdp2VRAMPages)
	}
	for i, p := range got {
		if p != uint32(i) {
			t.Fatalf("VRAM page %d taken as %d", i, p)
		}
	}
	if got := v.cramDirty.take(pages[:0]); !slices.Equal(got, []uint32{0, 1, 2, 3}) {
		t.Fatalf("markAll CRAM pages = %v", got)
	}
}

// TestPageDirtyTakeNoAllocs: a take of every page appends into the
// caller's slice without allocating.
func TestPageDirtyTakeNoAllocs(t *testing.T) {
	v := NewVDP2(NewSCU())
	pages := make([]uint32, 0, vdp2VRAMPages)
	if n := testing.AllocsPerRun(10, func() {
		v.markAllPages()
		pages = v.vramDirty.take(pages[:0])
		pages = v.cramDirty.take(pages[:0])
	}); n != 0 {
		t.Errorf("take: %.0f allocations", n)
	}
}
