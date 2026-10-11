// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestResolveNBGLineScaled checks the row values at scale: the row's
// scroll X and source Y are the native values carried at the scale
// shift more fraction bits, and sub-row k of the native row advances
// the source Y by k vertical increments at that precision, unless a
// vertical mosaic block replicates its rows. A bitmap's source row is
// that Y's integer part, wrapped to the bitmap's height.
func TestResolveNBGLineScaled(t *testing.T) {
	vram := new(compute.VDP2VRAM)
	for _, bitmap := range []bool{false, true} {
		for _, mosaicV := range []int32{0, 2} {
			native := NBGParams{
				Width: 320, Y: 5, DispY: 5, PlanePagesH: 1, PlanePagesV: 1,
				MosaicV: mosaicV, ScrollXFP: 37<<8 | 0x40, ScrollYBaseFP: 11<<8 | 0x80,
				IncXFP: 0x100, IncYFP: 0x0C0,
			}
			native.Screen.Bitmap = bitmap
			native.Screen.BmpWidth, native.Screen.BmpHeight = 512, 256
			ResolveNBGLine(vram, &native)
			for shift := uint32(1); shift <= 3; shift++ {
				n := int32(1) << shift
				for k := range n {
					p := native
					p.Screen.ScaleShift = shift
					p.SubRow = k
					ResolveNBGLine(vram, &p)
					if p.LineScrollXFP != native.LineScrollXFP<<shift || p.LineIncXFP != native.LineIncXFP {
						t.Fatalf("bitmap %v shift %d: scroll X %#x inc %#x, want %#x and the native %#x", bitmap, shift, p.LineScrollXFP, p.LineIncXFP, native.LineScrollXFP<<shift, native.LineIncXFP)
					}
					advance := k * native.IncYFP
					if mosaicV != 0 {
						advance = 0
					}
					if !bitmap {
						if want := native.BaseSyFP<<shift + advance; p.BaseSyFP != want {
							t.Fatalf("mosaic %d shift %d sub-row %d: source Y %#x, want %#x", mosaicV, shift, k, p.BaseSyFP, want)
						}
						continue
					}
					nativeY := native.ScrollYBaseFP + int32(native.DispY/max(mosaicV, 1)*max(mosaicV, 1))*native.IncYFP
					want := (nativeY<<shift + advance) >> (8 + shift) % 256
					if p.BmpSy != want {
						t.Fatalf("bitmap mosaic %d shift %d sub-row %d: source row %d, want %d", mosaicV, shift, k, p.BmpSy, want)
					}
				}
			}
		}
	}
}

// TestResolveNBGLineScaledLineScrollY checks the source Y at scale
// under line scroll Y: sub-row k advances by k vertical increments on
// a line whose next line is in the same table entry, and repeats the
// row on the entry's last line, every line at an interval of 1, since
// the next entry sets its line's source Y freely.
func TestResolveNBGLineScaledLineScrollY(t *testing.T) {
	vram := new(compute.VDP2VRAM)
	for _, interval := range []int32{1, 2, 4, 8} {
		for dispY := int32(0); dispY < 16; dispY++ {
			native := NBGParams{
				Width: 320, Y: dispY, DispY: dispY, PlanePagesH: 1, PlanePagesV: 1,
				LineScrollY: true, LineInterval: interval, LineTableAddr: 0x1000,
				ScrollYBaseFP: 11<<8 | 0x80, IncXFP: 0x100, IncYFP: 0x0C0,
			}
			ResolveNBGLine(vram, &native)
			for shift := uint32(1); shift <= 3; shift++ {
				for k := range int32(1) << shift {
					p := native
					p.Screen.ScaleShift = shift
					p.SubRow = k
					ResolveNBGLine(vram, &p)
					advance := k * native.IncYFP
					if dispY%interval == interval-1 {
						advance = 0
					}
					if want := native.BaseSyFP<<shift + advance; p.BaseSyFP != want {
						t.Fatalf("interval %d line %d shift %d sub-row %d: source Y %#x, want %#x", interval, dispY, shift, k, p.BaseSyFP, want)
					}
				}
			}
		}
	}
}
