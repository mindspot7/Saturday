// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// eraseOp builds an erase op of the inclusive rectangle in a frame
// buffer of the given width and depth, its height the buffer's rows
// in that format as the walk sets it.
func eraseOp(width int32, is8bpp bool, x1, y1, x3, y3 int32, color uint32) DrawOp {
	op := DrawOp{Kind: OpErase, Color: color}
	op.Pixel.FBWidth = width
	op.Pixel.Is8bpp = is8bpp
	if width > 0 {
		rowBytes := width * 2
		if is8bpp {
			rowBytes = width
		}
		op.Pixel.FBHeight = int32(compute.VDP1FBSize) / rowBytes
	}
	op.X[0], op.Y[0] = x1, y1
	op.X[2], op.Y[2] = x3, y3
	return op
}

// TestEraseRows16 checks the 16-bit fill: EWDR at every dot of the
// rectangle and nothing outside it.
func TestEraseRows16(t *testing.T) {
	fb := make(compute.VDP1FB, compute.VDP1FBSize)
	op := eraseOp(512, false, 8, 2, 11, 3, 0x1234)
	if got := OpLines(&op); got != 2 {
		t.Fatalf("OpLines = %d, want 2", got)
	}
	if got := OpDots(&op); got != 4 {
		t.Fatalf("OpDots = %d, want 4", got)
	}
	EraseRows(fb, &op, 0, OpLines(&op))
	for y := int32(0); y < 6; y++ {
		for x := int32(0); x < 16; x++ {
			want := uint16(0)
			if x >= 8 && x <= 11 && y >= 2 && y <= 3 {
				want = 0x1234
			}
			if got := fb.U16BE(uint32(y*512+x) * 2); got != want {
				t.Errorf("(%d,%d) = %#04x, want %#04x", x, y, got, want)
			}
		}
	}
}

// TestEraseRows8 checks the 8-bit fill: the high byte of EWDR on even
// columns and the low byte on odd, over a 1024-wide buffer.
func TestEraseRows8(t *testing.T) {
	fb := make(compute.VDP1FB, compute.VDP1FBSize)
	op := eraseOp(1024, true, 0, 0, 31, 2, 0xAABB)
	EraseRows(fb, &op, 0, OpLines(&op))
	for y := int32(0); y < 4; y++ {
		for x := int32(0); x < 34; x++ {
			want := uint8(0)
			if x <= 31 && y <= 2 {
				want = 0xAA
				if x&1 == 1 {
					want = 0xBB
				}
			}
			if got := fb.U8(uint32(y*1024 + x)); got != want {
				t.Errorf("(%d,%d) = %#02x, want %#02x", x, y, got, want)
			}
		}
	}
}

// TestEraseRowsClamp checks the row range clamp: rows past the
// rectangle are not erased.
func TestEraseRowsClamp(t *testing.T) {
	fb := make(compute.VDP1FB, compute.VDP1FBSize)
	op := eraseOp(512, false, 0, 0, 3, 1, 0xFFFF)
	EraseRows(fb, &op, 1, 10)
	if got := fb.U16BE(0); got != 0 {
		t.Errorf("row 0 erased before the range: %#04x", got)
	}
	if got := fb.U16BE(512 * 2); got != 0xFFFF {
		t.Errorf("row 1 = %#04x, want 0xFFFF", got)
	}
	if got := fb.U16BE(2 * 512 * 2); got != 0 {
		t.Errorf("row 2 erased past the rectangle: %#04x", got)
	}
}

// TestEraseRowsFBHeight checks that the op's frame buffer height, not
// the buffer's bytes, bounds the rectangle's rows: a buffer taller
// than the op's height takes no row past it.
func TestEraseRowsFBHeight(t *testing.T) {
	op := eraseOp(512, false, 0, 0, 3, 99, 0)
	op.Pixel.FBHeight = 100
	if got := OpLines(&op); got != 100 {
		t.Errorf("rectangle to the last row: OpLines = %d, want 100", got)
	}
	op.Y[2] = 100
	if got := OpLines(&op); got != 0 {
		t.Errorf("rectangle past the height: OpLines = %d, want 0", got)
	}
}

// TestEraseOpBounds checks that a rectangle outside the buffer the
// op's format describes has no lines and no dots, so a corrupt record
// draws nothing.
func TestEraseOpBounds(t *testing.T) {
	cases := []struct {
		name string
		op   DrawOp
	}{
		{"column past the width", eraseOp(512, false, 0, 0, 512, 0, 0)},
		{"negative column", eraseOp(512, false, -1, 0, 3, 0, 0)},
		{"reversed columns", eraseOp(512, false, 5, 0, 3, 0, 0)},
		{"row past a 16-bit buffer", eraseOp(512, false, 0, 0, 3, 256, 0)},
		{"row past an 8-bit 1024 buffer", eraseOp(1024, true, 0, 0, 3, 256, 0)},
		{"row past an 8-bit 512 buffer", eraseOp(512, true, 0, 0, 3, 512, 0)},
		{"negative row", eraseOp(512, false, 0, -1, 3, 3, 0)},
		{"reversed rows", eraseOp(512, false, 0, 4, 3, 3, 0)},
		{"zero width", eraseOp(0, false, 0, 0, 0, 0, 0)},
		{"negative width", eraseOp(-512, false, 0, 0, 0, 0, 0)},
	}
	for _, c := range cases {
		if got := OpLines(&c.op); got != 0 {
			t.Errorf("%s: OpLines = %d, want 0", c.name, got)
		}
		if got := OpDots(&c.op); got != 0 {
			t.Errorf("%s: OpDots = %d, want 0", c.name, got)
		}
	}
	inside := eraseOp(512, true, 0, 0, 511, 511, 0)
	if got := OpLines(&inside); got != 512 {
		t.Errorf("8-bit 512-wide buffer: OpLines = %d, want 512", got)
	}
}
