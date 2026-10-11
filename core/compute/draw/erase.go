// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// EraseRows erases rows [r0, r1) of the erase op's rectangle, r1
// clamped to the rectangle's rows, with EWDR: the 16-bit modes store
// EWDR per dot, the 8-bit modes its high byte on even columns and
// its low byte on odd (VDP1 manual Sec 4.4). The column parity is
// the native column's, the column shifted right by ScaleShift, so
// every byte of a native pixel's block holds that pixel's byte.
func EraseRows(fb compute.VDP1FB, op *DrawOp, r0, r1 int32) {
	rows := OpLines(op)
	cols := OpDots(op)
	if r1 > rows {
		r1 = rows
	}
	width := op.Pixel.FBWidth
	if op.Pixel.Is8bpp {
		hi := uint8(op.Color >> 8)
		lo := uint8(op.Color)
		shift := op.Pixel.ScaleShift
		for r := r0; r < r1; r++ {
			index := uint32((op.Y[0]+r)*width + op.X[0])
			for c := range cols {
				if (op.X[0]+c)>>shift&1 == 0 {
					fb.PutU8(index, hi)
				} else {
					fb.PutU8(index, lo)
				}
				index++
			}
		}
		return
	}
	color := uint16(op.Color)
	for r := r0; r < r1; r++ {
		off := uint32((op.Y[0]+r)*width+op.X[0]) * 2
		for range cols {
			fb.PutU16BE(off, color)
			off += 2
		}
	}
}
