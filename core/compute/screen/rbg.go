// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// screenOverArea is the display area screen-over mode 3 sets on each
// axis, 0 to 511, regardless of the plane size or bitmap size (VDP2
// manual, Plane Size Register, RxOVR=11).
const screenOverArea = int32(512)

// RotParam is one rotation parameter set as the row sees it: the
// host has applied the per-line re-reads and stepping, so these are
// the row's values (VDP2 manual Sec 6.1). The row start, the center,
// and the row coefficient address carry the screen's ScaleShift more
// fraction bits than their native .10; the steps stay .10.
type RotParam struct {
	Xsp, Ysp    int64 // row start in map space, .10 plus ScaleShift
	DX, DY      int64 // per-dot step, .10
	Xp, Yp      int64 // rotation center, .10 plus ScaleShift
	Kx, Ky      int64 // scale, .16
	DKAx        int64 // coefficient address step per dot, .10
	LineKAst    int64 // row coefficient address, .10 plus ScaleShift
	LineCoef    int64 // row coefficient value when per-dot reads are not allowed
	LineCoefLC  uint32
	LineCoefMSB bool
	CoefEn      bool
	CoefOneWord bool
	CoefMode    uint32 // 0 kx and ky, 1 kx, 2 ky, 3 Xp
	KLCE        bool   // line color from the coefficient
	KTAOF       uint32 // coefficient table offset bits
	ScreenOver  uint32 // 0 wrap, 1 pattern, 2 transparent, 3 force 512x512
	OVPNR       uint32 // screen-over pattern name
	MapOffset   uint32
	MapRegs     [16]uint32
	PlanePagesH int32 // page count used for the plane's page offset
	PlaneCellsH int32 // cells per plane
	PlaneCellsV int32
	TotalPixH   int32 // map size in dots
	TotalPixV   int32
	BitmapBase  uint32
}

// RPWindow is the rotation parameter window resolved for the row:
// each of W0 and W1 as an enable, an area bit, and the row's X range
// (the row is outside when On is false), plus the logic bit.
// Parameter B shows in the active area (Sec 6).
type RPWindow struct {
	En, Area, On [2]bool
	SX, EX       [2]int32
	LogicAnd     bool
}

// RBGParams is a rotation scroll screen's configuration for one row:
// the screen's dot params, the rotation parameter selection, and the
// two parameter sets as the row sees them.
type RBGParams struct {
	Width     int32
	Y         int32 // row of the layer and line color buffers
	DispY     int32
	HcntShift uint32 // 1 in hi-res: two native dots share one rotation step; at scale each native dot's sub-dots step within it

	Screen  ScreenParams
	MosaicH int32

	RPMode    uint32  // RPMD: 0 A, 1 B, 2 A with coefficient switch to B, 3 window
	CRKTE     bool    // coefficients from CRAM
	CoefDotOK [4]bool // per VRAM bank: the bank may be read for per-dot coefficients
	Window    RPWindow
	Param     [2]RotParam // A, B
}

// PrepareRBGRow derives the row constants the spans start from.
func PrepareRBGRow(p *RBGParams) {
	prepareScreenRow(&p.Screen)
}

// RPWindowB reports whether the rotation parameter window selects
// parameter B at dot x of the row.
func RPWindowB(w *RPWindow, x int32) bool {
	if !w.En[0] && !w.En[1] {
		// With both enables off the active area is the whole screen
		// under AND and nothing under OR (Sec 8.1).
		return w.LogicAnd
	}
	var act [2]bool
	for i := range int32(2) {
		if !w.En[i] {
			continue
		}
		inside := w.On[i] && x >= w.SX[i] && x <= w.EX[i]
		act[i] = inside != w.Area[i]
	}
	if w.En[0] && w.En[1] {
		if w.LogicAnd {
			return act[0] && act[1]
		}
		return act[0] || act[1]
	}
	if w.En[0] {
		return act[0]
	}
	return act[1]
}

// CoefAddr converts a .10 coefficient address to a VRAM byte address
// per Sec 6.1: the integer part indexes the table, scaled by the
// entry size and offset by KTAOF.
func CoefAddr(kastFP int64, ktaof uint32, oneWord bool) uint32 {
	idx := uint32(kastFP >> 10)
	if oneWord {
		return (ktaof&0x07)*0x20000 + idx*2
	}
	return (ktaof&0x03)*0x40000 + idx*4
}

// Coefficient reads a coefficient table entry from VRAM, or from the
// upper half of CRAM when CRKTE is set: the value (.16 for the scale
// modes, .10 for the Xp mode), the MSB (transparency or parameter
// switch), and the two-word format's 7-bit line color data.
func Coefficient(vram *compute.VDP2VRAM, cram *compute.CRAM, addr uint32, oneWord, mode3, fromCRAM bool) (val int64, msb bool, lc uint32) {
	var w0, w1 uint32
	if fromCRAM {
		w0 = uint32(cram.U16BE(0x800 + (addr & 0x7FF)))
	} else {
		w0 = uint32(vram.U16BE(addr))
	}
	msb = w0&0x8000 != 0
	if oneWord {
		if !mode3 {
			val = int64((w0>>10)&0x1F)<<10 | int64(w0&0x03FF)
			if val&(1<<14) != 0 {
				val |= ^int64(0x7FFF)
			}
			return val << 6, msb, 0
		}
		val = int64(w0 & 0x7FFF)
		if val&(1<<14) != 0 {
			val |= ^int64(0x7FFF)
		}
		return val << 8, msb, 0
	}
	if fromCRAM {
		w1 = uint32(cram.U16BE(0x800 + ((addr + 2) & 0x7FF)))
	} else {
		w1 = uint32(vram.U16BE(addr + 2))
	}
	lc = (w0 >> 8) & 0x7F
	if !mode3 {
		val = int64(w0&0xFF)<<16 | int64(w1)
		if val&(1<<23) != 0 {
			val |= ^int64(0xFFFFFF)
		}
		return val, msb, lc
	}
	val = int64(w0&0xFF)<<16 | int64(w1>>8)<<8 | int64(w1&0xFF)
	if val&(1<<23) != 0 {
		val |= ^int64(0xFFFFFF)
	}
	return val << 2, msb, lc
}

// rbgDot resolves a dot's map coordinate: which parameter the dot
// uses, the coefficient's effect on the scale or center, and the
// coefficient's transparency and line color. sel is the parameter
// after any coefficient switch and pages the parameter the window or
// mode selected (the page count follows it). transp is set when the
// coefficient marks the dot transparent.
func rbgDot(vram *compute.VDP2VRAM, cram *compute.CRAM, lcb compute.LineColor, p *RBGParams, lcIdx uint32, x int32, hcnt int64) (sel, pages int32, mapX, mapY int32, transp bool) {
	useA := true
	switch p.RPMode {
	case 1:
		useA = false
	case 3:
		useA = !RPWindowB(&p.Window, x)
	}
	sel = 1
	if useA {
		sel = 0
	}
	pages = sel
	// rp indexes the selected parameter set for the rest of the dot.
	rp := sel & 1
	kx, ky, xp := p.Param[rp].Kx, p.Param[rp].Ky, p.Param[rp].Xp
	crkte := p.CRKTE

	// The row start, center, and coefficient address are carried at
	// ScaleShift more fraction bits than the steps; hcnt is the scaled
	// column, so the native steps accumulate at that precision.
	shift := p.Screen.ScaleShift
	if p.Param[rp].CoefEn {
		addr := CoefAddr((p.Param[rp].LineKAst+p.Param[rp].DKAx*hcnt)>>shift, p.Param[rp].KTAOF, p.Param[rp].CoefOneWord)
		val, msb, lc := p.Param[rp].LineCoef, p.Param[rp].LineCoefMSB, p.Param[rp].LineCoefLC
		if crkte || p.CoefDotOK[(addr&(compute.VDP2VRAMSize-1))>>17] {
			val, msb, lc = Coefficient(vram, cram, addr, p.Param[rp].CoefOneWord, p.Param[rp].CoefMode == 3, crkte)
		}
		if p.RPMode == 2 && useA && msb {
			// Parameter A's coefficient MSB switches the dot to
			// parameter B (Sec 6.1, Table 6.4); B's own coefficient
			// then applies and A's value and line color do not.
			sel = 1
			rp = 1
			kx, ky, xp = p.Param[1].Kx, p.Param[1].Ky, p.Param[1].Xp
			if p.Param[1].CoefEn {
				addrB := CoefAddr((p.Param[1].LineKAst+p.Param[1].DKAx*hcnt)>>shift, p.Param[1].KTAOF, p.Param[1].CoefOneWord)
				valB, msbB, lcB := p.Param[1].LineCoef, p.Param[1].LineCoefMSB, p.Param[1].LineCoefLC
				if crkte || p.CoefDotOK[(addrB&(compute.VDP2VRAMSize-1))>>17] {
					valB, msbB, lcB = Coefficient(vram, cram, addrB, p.Param[1].CoefOneWord, p.Param[1].CoefMode == 3, crkte)
				}
				if p.Param[1].KLCE {
					lcb.PutU8(lcIdx, uint8(lcB)|0x80)
				}
				if msbB {
					return sel, pages, 0, 0, true
				}
				switch p.Param[1].CoefMode {
				case 0:
					kx, ky = valB, valB
				case 1:
					kx = valB
				case 2:
					ky = valB
				case 3:
					// The coefficient's Xp is at the table's 10
					// fraction bits; Xp is carried at ScaleShift more.
					xp = valB << shift
				}
			}
		} else {
			if p.Param[rp].KLCE {
				lcb.PutU8(lcIdx, uint8(lc)|0x80)
			}
			if msb {
				return sel, pages, 0, 0, true
			}
			switch p.Param[rp].CoefMode {
			case 0:
				kx, ky = val, val
			case 1:
				kx = val
			case 2:
				ky = val
			case 3:
				xp = val << shift
			}
		}
	}

	xRaw := p.Param[rp].Xsp + p.Param[rp].DX*hcnt
	yRaw := p.Param[rp].Ysp + p.Param[rp].DY*hcnt
	mapX = int32(((kx*xRaw)>>16 + xp) >> (10 + shift))
	mapY = int32(((ky*yRaw)>>16 + p.Param[rp].Yp) >> (10 + shift))
	return sel, pages, mapX, mapY, false
}

// RBGCellSpan renders dots [x0, x1) of the row of a cell-mode
// rotation scroll screen into the layer buffer, writing coefficient
// line color data into lcb. Entry point.
func RBGCellSpan(vram *compute.VDP2VRAM, cram *compute.CRAM, pal *compute.Palette, out compute.Layer, lcb compute.LineColor, p *RBGParams, x0, x1 int32) {
	entrySize := p.Screen.EntrySize
	charPx, mapDim := p.Screen.CharPx, p.Screen.MapDim
	pageBoundary := p.Screen.PageBoundary
	row := uint32(p.Y * p.Width)
	cramMode, cramOffset, transpOff := p.Screen.CRAMMode, p.Screen.CRAMOffset, p.Screen.TranspOff
	plain, plainBase := p.Screen.Plain, p.Screen.PlainBase
	shift := p.Screen.ScaleShift

	for x := x0; x < x1; x++ {
		ex := x
		if p.MosaicH > 1 {
			ex = (x / p.MosaicH) * p.MosaicH
		}
		hcnt := int64((ex>>(shift+p.HcntShift))<<shift | ex&(1<<shift-1))
		sel, pages, mapX, mapY, coefTransp := rbgDot(vram, cram, lcb, p, row+uint32(x), x, hcnt)
		if coefTransp {
			out[row+uint32(x)] = 0
			continue
		}

		// The selected parameter set's fields, read once per dot.
		rp := sel & 1
		totalPixH, totalPixV := p.Param[rp].TotalPixH, p.Param[rp].TotalPixV
		planeCellsH, planeCellsV := p.Param[rp].PlaneCellsH, p.Param[rp].PlaneCellsV
		// Screen-over (Plane Size Register RxOVR): mode 3 sets the
		// display area to screenOverArea dots on each axis regardless
		// of the plane size, transparent past it; the other modes act
		// past the map.
		over := false
		screenOver := p.Param[rp].ScreenOver
		if screenOver == 3 {
			if mapX < 0 || mapX >= screenOverArea || mapY < 0 || mapY >= screenOverArea {
				out[row+uint32(x)] = 0
				continue
			}
		} else if mapX < 0 || mapX >= totalPixH || mapY < 0 || mapY >= totalPixV {
			switch screenOver {
			case 0:
				mapX = ((mapX % totalPixH) + totalPixH) % totalPixH
				mapY = ((mapY % totalPixV) + totalPixV) % totalPixV
			case 2:
				out[row+uint32(x)] = 0
				continue
			case 1:
				over = true
			}
		}

		var charNum, palette uint32
		var hflip, vflip, specialPri, specialCC bool
		var dotX, dotY int32
		if over {
			// The screen-over pattern from OVPNRA/OVPNRB of the
			// selected parameter repeats in displayed-line space, at
			// the native dot's column.
			charNum, palette, hflip, vflip, specialPri, specialCC = patternName1(&p.Screen, p.Param[pages].OVPNR)
			dotX, dotY = (x>>p.Screen.ScaleShift)%charPx, p.DispY%charPx
		} else {
			cellX := mapX / charPx
			cellY := mapY / charPx
			dotX = mapX & (charPx - 1)
			dotY = mapY & (charPx - 1)
			planeX := (cellX / planeCellsH) % 4
			planeY := (cellY / planeCellsV) % 4
			planeIdx := planeY*4 + planeX
			localCellX := cellX % planeCellsH
			localCellY := cellY % planeCellsV
			pageX := localCellX / mapDim
			pageY := localCellY / mapDim
			pageCellX := localCellX % mapDim
			pageCellY := localCellY % mapDim
			combined := p.Param[rp].MapRegs[planeIdx&15]&0x3F | p.Param[rp].MapOffset<<6
			pagesH := p.Param[pages].PlanePagesH
			addr := combined*pageBoundary + uint32(pageY*pagesH+pageX)*pageBoundary + uint32(pageCellY*mapDim+pageCellX)*entrySize
			if p.Screen.PNWord1 {
				charNum, palette, hflip, vflip, specialPri, specialCC = patternName1(&p.Screen, uint32(vram.U16BE(addr)))
			} else {
				charNum, palette, hflip, vflip, specialPri, specialCC = DecodePattern2Word(uint32(vram.U16BE(addr)), uint32(vram.U16BE(addr+2)))
			}
		}

		cellAddr, dx, dy := cellOrigin(&p.Screen, charNum, hflip, vflip, dotX, dotY)
		var rgb, dotColor uint32
		var transp, cramCC bool
		switch p.Screen.ColorMode {
		case 0:
			dotColor = CellDot4(vram, cellAddr, dx, dy)
			rgb, transp, cramCC = PaletteDot(pal, cramMode, dotColor, palette, cramOffset, 0, transpOff)
		case 1:
			dotColor = CellDot8(vram, cellAddr, dx, dy)
			rgb, transp, cramCC = PaletteDot(pal, cramMode, dotColor, palette, cramOffset, 1, transpOff)
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
			out[row+uint32(x)] = specialPixel(&p.Screen, rgb, transp, dotColor, specialPri, specialCC, cramCC)
		}
	}
}

// RBGBitmapSpan renders dots [x0, x1) of the row of a bitmap-mode
// rotation scroll screen into the layer buffer. Screen-over wrap
// repeats the bitmap, mode 3 repeats it over the screenOverArea
// display area and is transparent past it (Plane Size Register
// note: a 256-dot bitmap shows two images), and the other modes
// leave an out-of-bitmap dot transparent. Entry point.
func RBGBitmapSpan(vram *compute.VDP2VRAM, cram *compute.CRAM, pal *compute.Palette, out compute.Layer, lcb compute.LineColor, p *RBGParams, x0, x1 int32) {
	row := uint32(p.Y * p.Width)
	bmpW, bmpH := p.Screen.BmpWidth, p.Screen.BmpHeight
	cramMode, cramOffset, transpOff := p.Screen.CRAMMode, p.Screen.CRAMOffset, p.Screen.TranspOff
	palette := p.Screen.BmpPalette
	specialPri, specialCC := p.Screen.BmpSpecialPri, p.Screen.BmpSpecialCC
	plain, plainBase := p.Screen.Plain, p.Screen.PlainBase
	shift := p.Screen.ScaleShift
	for x := x0; x < x1; x++ {
		ex := x
		if p.MosaicH > 1 {
			ex = (x / p.MosaicH) * p.MosaicH
		}
		hcnt := int64((ex>>(shift+p.HcntShift))<<shift | ex&(1<<shift-1))
		sel, _, mapX, mapY, coefTransp := rbgDot(vram, cram, lcb, p, row+uint32(x), x, hcnt)
		if coefTransp {
			out[row+uint32(x)] = 0
			continue
		}
		screenOver := p.Param[sel&1].ScreenOver
		if screenOver == 3 && (mapX < 0 || mapX >= screenOverArea || mapY < 0 || mapY >= screenOverArea) {
			out[row+uint32(x)] = 0
			continue
		}
		if mapX < 0 || mapX >= bmpW || mapY < 0 || mapY >= bmpH {
			if screenOver != 0 && screenOver != 3 {
				out[row+uint32(x)] = 0
				continue
			}
			mapX = ((mapX % bmpW) + bmpW) % bmpW
			mapY = ((mapY % bmpH) + bmpH) % bmpH
		}
		base := p.Param[sel&1].BitmapBase
		dot := uint32(mapY*bmpW + mapX)
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
