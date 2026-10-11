// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// NBGParams is a normal scroll screen's configuration for one row:
// the screen's dot params, the register-decoded map and scroll setup,
// the frame's vertical counter base, and the row values
// ResolveNBGLine derives from the line scroll table.
type NBGParams struct {
	Width  int32 // row width in dots
	Y      int32 // row of the layer buffer
	DispY  int32 // displayed line (2 * field line + field under double-density interlace)
	SubRow int32 // sub-row of the native row at scale, 0 at native

	Screen ScreenParams

	MapOffset   uint32 // MPOFN field
	MapRegs     [4]uint32
	PlanePagesH int32
	PlanePagesV int32
	MosaicH     int32 // 0 off, else block width
	MosaicV     int32

	VCSCEnabled   bool
	VCSCTableAddr uint32
	VCSCStride    uint32

	LineScrollX   bool
	LineScrollY   bool
	LineZoomX     bool
	LineInterval  int32
	LineTableAddr uint32
	ScrollXFP     int32 // register scroll X, 11.8
	ScrollYBaseFP int32 // frame vertical counter base, 11.8
	IncXFP        int32 // register increments, 3.8
	IncYFP        int32

	// Row values from ResolveNBGLine.
	LineScrollXFP int32 // row scroll X (bitmap: wrapped to 11 bits)
	LineIncXFP    int32
	BaseSyFP      int32 // cell: row source Y before the per-column vertical cell scroll
	BmpSy         int32 // bitmap: source row after the height wrap

	// Row constants from ResolveNBGLine, so a span call starts with
	// loads rather than derivations (the software schedule calls a
	// span for every few dots).
	PlaneCellsH int32
	PlaneCellsV int32
	PlaneSizeH  int32 // plane size in dots
	PlaneSizeV  int32
}

// ResolveNBGLine derives the row constants and reads the row's line
// scroll table entry for the row's scroll X, X increment, and source
// Y. Per VDP2 manual Sec 5.3 the table values are relative to the
// scroll registers, and the vertical coordinate increment advances Y
// within an entry only when the entry spans two or more lines. The
// bitmap form wraps the scroll X sum to 11 bits and sign-extends it,
// so a scroll of 2032 places the bitmap 16 dots left rather than at
// an aliased position.
//
// At scale the row's scroll X and source Y are carried at ScaleShift
// more fraction bits, the bitmap's scroll X shifted after its wrap,
// and the source Y advances by SubRow vertical increments at that
// precision, which is sub-row SubRow's own position within the native
// row, where the next line's source Y is the row's plus one increment.
// A vertical mosaic block replicates its rows, so there it does not
// advance, and neither does it on the last line of a line scroll Y
// entry (every line at an interval of 1), since the next line's source
// Y is the next entry's, which the table sets freely: a row's sub-rows
// then repeat the row. The X increment stays native: a scaled column's
// step at the wider fraction is the native step.
func ResolveNBGLine(vram *compute.VDP2VRAM, p *NBGParams) {
	shift := p.Screen.ScaleShift
	prepareScreenRow(&p.Screen)
	p.PlaneCellsH = p.Screen.MapDim * p.PlanePagesH
	p.PlaneCellsV = p.Screen.MapDim * p.PlanePagesV
	p.PlaneSizeH = p.PlaneCellsH * p.Screen.CharPx
	p.PlaneSizeV = p.PlaneCellsV * p.Screen.CharPx

	hasLineScroll := p.LineScrollX || p.LineScrollY || p.LineZoomX
	lsxDelta := int32(0)
	lsyDelta := int32(0)
	p.LineIncXFP = p.IncXFP
	if hasLineScroll {
		lineIdx := p.DispY / p.LineInterval
		var stride uint32
		if p.LineScrollX {
			stride += 4
		}
		if p.LineScrollY {
			stride += 4
		}
		if p.LineZoomX {
			stride += 4
		}
		off := p.LineTableAddr + uint32(lineIdx)*stride
		if p.LineScrollX {
			lsxDelta = int32(vram.U16BE(off)&0x07FF)<<8 | int32(vram.U16BE(off+2)>>8)
			off += 4
		}
		if p.LineScrollY {
			lsyDelta = int32(vram.U16BE(off)&0x07FF)<<8 | int32(vram.U16BE(off+2)>>8)
			off += 4
		}
		if p.LineZoomX {
			p.LineIncXFP = int32(vram.U16BE(off)&0x07)<<8 | int32(vram.U16BE(off+2)>>8)
		}
	}

	ey := p.DispY
	if p.MosaicV > 0 {
		ey = (p.DispY / p.MosaicV) * p.MosaicV
	}
	yAdvance := ey
	if hasLineScroll && p.LineScrollY {
		yAdvance = 0
		if p.LineInterval > 1 {
			yAdvance = ey % p.LineInterval
		}
	}

	syFP := (p.ScrollYBaseFP + lsyDelta + yAdvance*p.IncYFP) << shift
	if p.MosaicV == 0 && (!p.LineScrollY || yAdvance < p.LineInterval-1) {
		syFP += p.SubRow * p.IncYFP
	}
	if !p.Screen.Bitmap {
		p.LineScrollXFP = (p.ScrollXFP + lsxDelta) << shift
		p.BaseSyFP = syFP
		return
	}
	xInt := (p.ScrollXFP >> 8) + (lsxDelta >> 8)
	xFrac := (p.ScrollXFP & 0xFF) + (lsxDelta & 0xFF)
	xInt += xFrac >> 8
	xInt &= 0x7FF
	if xInt >= 0x400 {
		xInt -= 0x800
	}
	p.LineScrollXFP = (xInt<<8 | xFrac&0xFF) << shift
	sy := syFP >> (8 + shift)
	if p.Screen.BmpHeight > 0 {
		sy %= p.Screen.BmpHeight
		if sy < 0 {
			sy += p.Screen.BmpHeight
		}
	}
	p.BmpSy = sy
}

// NBGCellState is the cell span's row state: the last cell whose
// pattern name was fetched and its decode, kept across the software
// schedule's span calls on a row so a run of dots in one cell decodes
// it once. It starts empty for each row (and for each GPU thread) and
// is an optimization only: a miss recomputes.
type NBGCellState struct {
	HaveCell bool
	SxCell   int32
	SyCell   int32
	CharNum  uint32
	Palette  uint32
	Flags    uint32 // bit 0 hflip, 1 vflip, 2 special priority, 3 special CC
}

// NBGCellSpan renders dots [x0, x1) of the row of a cell-mode normal
// scroll screen into the layer buffer. Entry point: the software
// renderer calls it per span, the GPU per segment of whole cells at
// the scale.
func NBGCellSpan(vram *compute.VDP2VRAM, pal *compute.Palette, out compute.Layer, p *NBGParams, st *NBGCellState, x0, x1 int32) {
	entrySize := p.Screen.EntrySize
	charPx, mapDim := p.Screen.CharPx, p.Screen.MapDim
	charMask := charPx - 1
	mapMask := mapDim - 1
	pageBoundary := p.Screen.PageBoundary
	planeCellsH, planeCellsV := p.PlaneCellsH, p.PlaneCellsV
	planeSizeH, planeSizeV := p.PlaneSizeH, p.PlaneSizeV
	row := uint32(p.Y * p.Width)
	cramMode, cramOffset, transpOff := p.Screen.CRAMMode, p.Screen.CRAMOffset, p.Screen.TranspOff
	plain, plainBase := p.Screen.Plain, p.Screen.PlainBase
	// A source position's integer part is above its 8 fraction bits
	// and the scale shift's; a native column is 1 << ScaleShift dots.
	shift := p.Screen.ScaleShift
	fracBits := 8 + shift

	// The plane and page addressing and the pattern name decode
	// depend only on the cell, so they run when the cell changes.
	haveCell := st.HaveCell
	pSxCell, pSyCell := st.SxCell, st.SyCell
	cCharNum, cPalette := st.CharNum, st.Palette
	cFlags := st.Flags

	for x := x0; x < x1; x++ {
		ex := x
		if p.MosaicH > 0 {
			ex = (x / p.MosaicH) * p.MosaicH
		}
		syFP := p.BaseSyFP
		if p.VCSCEnabled {
			vcOff := p.VCSCTableAddr + uint32(ex>>(3+shift))*p.VCSCStride
			syFP += (int32(vram.U16BE(vcOff)&0x07FF)<<8 | int32(vram.U16BE(vcOff+2)>>8)) << shift
		}
		sy := syFP >> fracBits
		dotY := sy & charMask
		sx := (p.LineScrollXFP + ex*p.LineIncXFP) >> fracBits
		dotX := sx & charMask
		sxCell := sx / charPx
		syCell := sy / charPx

		if !haveCell || sxCell != pSxCell || syCell != pSyCell {
			planeV := (sy / planeSizeV) & 1
			planeCellY := (sy / charPx) % planeCellsV
			planeH := (sx / planeSizeH) & 1
			planeCellX := (sx / charPx) % planeCellsH
			pageX := planeCellX / mapDim
			pageY := planeCellY / mapDim
			localCellX := planeCellX & mapMask
			localCellY := planeCellY & mapMask
			planeIdx := planeV*2 + planeH
			combined := p.MapRegs[planeIdx&3]&0x3F | p.MapOffset<<6
			addr := combined*pageBoundary + uint32(pageY*p.PlanePagesH+pageX)*pageBoundary + uint32(localCellY*mapDim+localCellX)*entrySize
			var hf, vf, sp, sc bool
			if p.Screen.PNWord1 {
				cCharNum, cPalette, hf, vf, sp, sc = patternName1(&p.Screen, uint32(vram.U16BE(addr)))
			} else {
				cCharNum, cPalette, hf, vf, sp, sc = DecodePattern2Word(uint32(vram.U16BE(addr)), uint32(vram.U16BE(addr+2)))
			}
			cFlags = 0
			if hf {
				cFlags |= 1
			}
			if vf {
				cFlags |= 2
			}
			if sp {
				cFlags |= 4
			}
			if sc {
				cFlags |= 8
			}
			pSxCell, pSyCell = sxCell, syCell
			haveCell = true
		}

		cellAddr, dx, dy := cellOrigin(&p.Screen, cCharNum, cFlags&1 != 0, cFlags&2 != 0, dotX, dotY)
		var rgb, dotColor uint32
		var transp, cramCC bool
		switch p.Screen.ColorMode {
		case 0:
			dotColor = CellDot4(vram, cellAddr, dx, dy)
			rgb, transp, cramCC = PaletteDot(pal, cramMode, dotColor, cPalette, cramOffset, 0, transpOff)
		case 1:
			dotColor = CellDot8(vram, cellAddr, dx, dy)
			rgb, transp, cramCC = PaletteDot(pal, cramMode, dotColor, cPalette, cramOffset, 1, transpOff)
		case 2:
			idx := CellDot16(vram, cellAddr, dx, dy) & 0x07FF
			dotColor = idx & 0xFF
			rgb, transp, cramCC = PaletteDot(pal, cramMode, idx, 0, cramOffset, 2, transpOff)
		case 3:
			raw := CellDot16(vram, cellAddr, dx, dy)
			if raw&0x8000 == 0 && !transpOff {
				transp = true
			} else {
				rgb = RGB555To888(raw)
			}
		case 4:
			var opaque bool
			rgb, opaque = CellDot32(vram, cellAddr, dx, dy)
			transp = !opaque && !transpOff
		}
		if plain {
			out[row+uint32(x)] = plainPixel(plainBase, rgb, transp)
		} else {
			out[row+uint32(x)] = specialPixel(&p.Screen, rgb, transp, dotColor, cFlags&4 != 0, cFlags&8 != 0, cramCC)
		}
	}
	st.HaveCell = haveCell
	st.SxCell, st.SyCell = pSxCell, pSyCell
	st.CharNum, st.Palette = cCharNum, cPalette
	st.Flags = cFlags
}

// NBGBitmapSpan renders dots [x0, x1) of the row of a bitmap-mode
// normal scroll screen into the layer buffer. The 16.7M-color bitmap
// reads linearly without the width wrap so line-scroll stride
// compensation lands pixels where the game placed them.
func NBGBitmapSpan(vram *compute.VDP2VRAM, pal *compute.Palette, out compute.Layer, p *NBGParams, x0, x1 int32) {
	base := p.MapOffset * 0x20000
	row := uint32(p.Y * p.Width)
	sy := p.BmpSy
	bmpW := p.Screen.BmpWidth
	cramMode, cramOffset, transpOff := p.Screen.CRAMMode, p.Screen.CRAMOffset, p.Screen.TranspOff
	palette := p.Screen.BmpPalette
	specialPri, specialCC := p.Screen.BmpSpecialPri, p.Screen.BmpSpecialCC
	plain, plainBase := p.Screen.Plain, p.Screen.PlainBase
	fracBits := 8 + p.Screen.ScaleShift
	for x := x0; x < x1; x++ {
		ex := x
		if p.MosaicH > 0 {
			ex = (x / p.MosaicH) * p.MosaicH
		}
		sx := (p.LineScrollXFP + ex*p.LineIncXFP) >> fracBits
		if p.Screen.ColorMode != 4 {
			sx %= bmpW
			if sx < 0 {
				sx += bmpW
			}
		}
		dot := uint32(sy*bmpW + sx)
		var rgb, dotColor uint32
		var transp, cramCC bool
		switch p.Screen.ColorMode {
		case 0:
			dotColor = BitmapDot4(vram, base, dot)
			rgb, transp, cramCC = PaletteDot(pal, cramMode, dotColor, palette, cramOffset, 0, transpOff)
		case 1:
			dotColor = BitmapDot8(vram, base, dot)
			rgb, transp, cramCC = PaletteDot(pal, cramMode, dotColor, palette, cramOffset, 1, transpOff)
		case 2:
			idx := BitmapDot16(vram, base, dot) & 0x07FF
			dotColor = idx & 0xFF
			rgb, transp, cramCC = PaletteDot(pal, cramMode, idx, 0, cramOffset, 2, transpOff)
		case 3:
			raw := BitmapDot16(vram, base, dot)
			if raw&0x8000 == 0 && !transpOff {
				transp = true
			} else {
				rgb = RGB555To888(raw)
			}
		case 4:
			var opaque bool
			rgb, opaque = BitmapDot32(vram, base, dot)
			transp = !opaque && !transpOff
		}
		if plain {
			out[row+uint32(x)] = plainPixel(plainBase, rgb, transp)
		} else {
			out[row+uint32(x)] = specialPixel(&p.Screen, rgb, transp, dotColor, specialPri, specialCC, cramCC)
		}
	}
}
