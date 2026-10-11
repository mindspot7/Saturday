// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestCopyPages runs the page copy entries over a band of two staged
// pages from a base slot and checks that each page lands whole at the
// page the table names, that the slot before the base is untouched,
// and that CRAM copies the same way.
func TestCopyPages(t *testing.T) {
	var vram compute.VDP2VRAM
	var store compute.PageStore
	var table compute.PageTable
	for i := range 3 * compute.PageBytes {
		store[i] = byte(i*7 + 1)
	}
	table[0], table[1], table[2] = 9, 5, 511
	p := CopyParams{Base: 1}
	pages := VRAMPages{Store: &store, Table: &table}
	for i := range uint32(2 * compute.PageWords) {
		CopyVRAMPages(&vram, &pages, &p, i)
	}
	for _, c := range []struct{ slot, page uint32 }{{1, 5}, {2, 511}} {
		for i := range uint32(compute.PageBytes) {
			if got, want := vram[c.page*compute.PageBytes+i], store[c.slot*compute.PageBytes+i]; got != want {
				t.Fatalf("VRAM page %d byte %d = %#x, want %#x from slot %d", c.page, i, got, want, c.slot)
			}
		}
	}
	for i := range uint32(compute.PageBytes) {
		if vram[9*compute.PageBytes+i] != 0 {
			t.Fatalf("VRAM page 9, the slot before the base, was written")
		}
	}

	var cram compute.CRAM
	var cstore compute.CRAMPageStore
	for i := range 2 * compute.PageBytes {
		cstore[i] = byte(i*3 + 2)
	}
	table[0], table[1] = 3, 1
	p = CopyParams{Base: 0}
	cpages := CRAMPages{Store: &cstore, Table: &table}
	for i := range uint32(2 * compute.PageWords) {
		CopyCRAMPages(&cram, &cpages, &p, i)
	}
	for _, c := range []struct{ slot, page uint32 }{{0, 3}, {1, 1}} {
		for i := range uint32(compute.PageBytes) {
			if got, want := cram[c.page*compute.PageBytes+i], cstore[c.slot*compute.PageBytes+i]; got != want {
				t.Fatalf("CRAM page %d byte %d = %#x, want %#x from slot %d", c.page, i, got, want, c.slot)
			}
		}
	}
	for i := range uint32(compute.PageBytes) {
		if cram[i] != 0 || cram[2*compute.PageBytes+i] != 0 {
			t.Fatal("a CRAM page outside the band was written")
		}
	}
}
