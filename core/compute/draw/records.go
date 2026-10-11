// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// Line records: one word per line of an op that reads texels, at
// the op's record base, holding where the line ended. A line's
// setup is a function of the op and the line index, so a renderer
// drawing the op recomputes it with the walk's own setup functions;
// what it cannot recompute is where the end code rule ended the
// line (VDP1 manual, End Code Disable: the row ends where its second
// end code is read), which the walk finds by reading the line's
// texels in order. The walk stores that word when the line ends. A
// sprite row's word is the column its span ended at: the row's width
// when no end code ended it, the second end code's column when one
// did, 0 for a scaled sprite row outside the drawing area. A
// connecting line's is its last step after the pre-clip: the
// pre-clipped last step when no end code ended it, the step before
// the second end code when one did, which is always below the
// pre-clipped last step. Polygons, polylines, lines, and erases read
// no texels and have no records. A record holds LineEndPending from
// the op's reservation until the walk ends the line, so a renderer
// drawing a line the walk has not ended can tell its record is not
// final; every final record is at least zero.
const LineRecordWords = 1

// LineEndPending is the record of a line the walk has not ended.
const LineEndPending = -1

// RecordStride is the words per line record of op's kind.
func RecordStride(op *DrawOp) uint32 {
	switch op.Kind {
	case OpNormalSprite, OpScaledSprite, OpDistortedSprite:
		return LineRecordWords
	}
	return 0
}

// OpRecordWords is the words op's records take: one record per line.
func OpRecordWords(op *DrawOp) uint32 {
	return uint32(OpLines(op)) * RecordStride(op)
}

// StoreLineEnd stores where line i of the op at base ended.
func StoreLineEnd(rec compute.LineRecords, base uint32, i, end int32) {
	rec[base+uint32(i)] = uint32(end)
}

// LoadLineEnd reads where line i of the op at base ended.
func LoadLineEnd(rec compute.LineRecords, base uint32, i int32) int32 {
	return int32(rec[base+uint32(i)])
}

// RecordedLineEnd is the exclusive end step of line line of op as the
// walk's record ended it, the end a renderer drawing the line applies
// to it (generate): a sprite row's recorded end column, through
// ScaledRowEnd at scale, where line is the scaled row, and a
// distorted sprite's recorded last step plus one. A line the walk has
// not ended is at or below zero, so the line draws nothing. A kind
// with no records (RecordStride zero) has no recorded end, -1.
func RecordedLineEnd(rec compute.LineRecords, op *DrawOp, line int32) int32 {
	switch op.Kind {
	case OpNormalSprite, OpScaledSprite:
		if op.Pixel.ScaleShift != 0 {
			return ScaledRowEnd(rec, op, line)
		}
		return LoadLineEnd(rec, op.RecordBase, line)
	case OpDistortedSprite:
		return LoadLineEnd(rec, op.RecordBase, line) + 1
	}
	return -1
}

// ceilDiv64 is a / b rounded up, for b above zero.
func ceilDiv64(a, b int64) int64 {
	return -floorDiv64(-a, b)
}

// A scaled line's end at ScaleShift above zero comes from the record
// of its native line: a scaled line's finer sampling reads columns
// the native line skipped, or a column more times, so its own end
// code count does not end it (EndsOnCount). A connecting line's
// sub-steps end with the blocks of the native steps (generate, and
// the step blocks in quad.go). A sprite row is row >> ScaleShift of the
// native op (nativeOp), and when the native row ended on its end
// codes the scaled row ends before its first column whose source
// column reaches the native row's ending column in the direction of
// travel. A row that checks end codes is never snapped
// to one parity (high speed shrink disables the check), so the
// column follows the unsnapped sampling. For character data laid out
// as the VDP1 manual requires (Sec 6.3 End Code Disable, ECD 0 with
// SPD 0 and only transparent pixels outside the end codes) the cut
// changes no pixel; it keeps the scaled line to the native line's
// dots on data that is not.

// ScaledRowEnd is the end column, exclusive, of scaled sprite row
// row of op: the row's width when the native row did not end on its
// end codes. The native row's ending sample c is its unflipped
// midpoint sample at the recorded column, and the scaled row's
// unflipped sample is at or past c from the first column x with
// (2x + 1) * CharW >= 2 * width * c. A normal sprite's sample is the
// column itself, which the same forms give at a width of CharW.
func ScaledRowEnd(rec compute.LineRecords, op *DrawOp, row int32) int32 {
	native := nativeOp(op)
	end := LoadLineEnd(rec, op.RecordBase, row>>op.Pixel.ScaleShift)
	width := SpriteRowWidth(op)
	nativeWidth := SpriteRowWidth(&native)
	if end < 0 || end >= nativeWidth {
		return width
	}
	if op.CharW <= 0 {
		// Every column samples column 0, the ending column.
		return 0
	}
	c := ((2*end + 1) * op.CharW) / (2 * nativeWidth)
	cw := int64(op.CharW)
	x := ceilDiv64(2*int64(width)*int64(c)-cw, 2*cw)
	return int32(max(x, 0))
}
