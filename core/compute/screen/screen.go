// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

// ScreenParams is what a scroll screen's dots share whichever screen
// draws them: the character and pattern name format, the color
// format, the priority and color calculation rules, the bitmap form,
// and the row constants prepareScreenRow derives. A normal and a
// rotation screen's row params each hold one, and the spans and the
// dot functions below read the screen through it.
type ScreenParams struct {
	ColorMode  uint32 // 0 16-color, 1 256, 2 2048, 3 32K, 4 16.7M
	Char2x2    bool   // 16x16 characters
	PNWord1    bool   // 1-word pattern names
	AuxMode1   bool   // auxiliary mode 1 pattern names
	PNCN       uint32 // pattern name control register
	Priority   uint32
	CRAMOffset uint32
	CRAMMode   uint32
	TranspOff  bool
	SFPRMD     uint32 // special priority mode
	SFCCMD     uint32 // special color calculation mode
	SFCode     uint32 // the screen's special function code
	ScreenCC   bool   // CCCTL enable for the screen

	// ScaleShift is log2 of the render scale N (N = 1 << ScaleShift),
	// 0 at native: a source position is the native fixed-point value
	// carried at ScaleShift more fraction bits, so the spans extract
	// its integer part with a shift that much wider.
	ScaleShift uint32

	Bitmap        bool
	BmpWidth      int32
	BmpHeight     int32
	BmpPalette    uint32
	BmpSpecialPri bool
	BmpSpecialCC  bool

	// Row constants from prepareScreenRow, so a span call starts with
	// loads rather than derivations (the software schedule calls a
	// span for every few dots).
	Plain        bool   // no special priority or color calculation mode
	PlainBase    uint32 // priority and screen CC bits for plain dots
	SubCellScale uint32 // character units between a 2x2 character's sub-cells
	EntrySize    uint32 // pattern name entry bytes
	CharPx       int32  // character size in dots
	MapDim       int32  // cells per page side
	PageBoundary uint32 // page bytes
}

// prepareScreenRow derives the row constants of the screen. A screen
// without a special priority or special color calculation mode packs
// every opaque dot as the row's priority and color calculation enable
// over the dot's color (Plain, PlainBase); the special modes decide
// per dot instead, and so does a priority of 0, which is transparent.
func prepareScreenRow(s *ScreenParams) {
	s.Plain = s.SFPRMD == 0 && s.SFCCMD == 0 && s.Priority != 0
	s.PlainBase = 0
	if s.Plain {
		s.PlainBase = s.Priority << 24
		if s.ScreenCC {
			s.PlainBase |= LayerCCBit
		}
	}
	s.SubCellScale = 1
	switch s.ColorMode {
	case 1:
		s.SubCellScale = 2
	case 2, 3:
		s.SubCellScale = 4
	case 4:
		s.SubCellScale = 8
	}
	s.EntrySize = 4
	if s.PNWord1 {
		s.EntrySize = 2
	}
	s.CharPx, s.MapDim = CellDots, 64
	if s.Char2x2 {
		s.CharPx, s.MapDim = 2*CellDots, 32
	}
	s.PageBoundary = uint32(s.MapDim*s.MapDim) * s.EntrySize
}

// patternName1 decodes a 1-word pattern name under the screen's
// supplement bits: the special priority and special color calculation
// bits come from PNCN. A span reads a 2-word entry through
// DecodePattern2Word in its own loop, since the rotation span reads
// one per dot and a call there costs as the dot decode's would.
func patternName1(s *ScreenParams, pn uint32) (charNum, palette uint32, hflip, vflip, specialPri, specialCC bool) {
	charNum, palette, hflip, vflip = DecodePattern1Word(pn, s.PNCN, s.ColorMode, s.AuxMode1, s.Char2x2)
	return charNum, palette, hflip, vflip, s.PNCN&(1<<9) != 0, s.PNCN&(1<<8) != 0
}

// cellOrigin is the cell a character dot reads and the dot's position
// in it: the flips reverse the axes, and a 2x2 character's dot
// selects its sub-cell after the flips.
//
// The color mode's read of the dot and its resolution stay in each
// span's loop as a switch: a function holding the switch is not
// inlined, and every loop value live across a call is spilled and
// reloaded per dot.
func cellOrigin(s *ScreenParams, charNum uint32, hflip, vflip bool, dotX, dotY int32) (cellAddr uint32, dx, dy int32) {
	dx, dy = dotX, dotY
	if !s.Char2x2 {
		if hflip {
			dx = 7 - dx
		}
		if vflip {
			dy = 7 - dy
		}
		return charNum * 0x20, dx, dy
	}
	if hflip {
		dx = 15 - dx
	}
	if vflip {
		dy = 15 - dy
	}
	charNum += uint32((dy/8)*2+dx/8) * s.SubCellScale
	return charNum * 0x20, dx & 7, dy & 7
}

// A span packs a resolved dot as the layer pixel through plainPixel
// when the screen is plain and specialPixel otherwise, the choice and
// the plain base held in the span's locals: the plain path is the
// loop's own code and the special modes are a call.

// plainPixel packs a plain screen's dot: 0 when transparent, else the
// row's base over the color.
func plainPixel(plainBase, rgb uint32, transp bool) uint32 {
	if transp {
		return 0
	}
	return plainBase | rgb
}

// specialPixel resolves a dot's priority and color calculation
// enable under the screen's special modes and packs the layer pixel,
// 0 when transparent. The special priority modes rewrite the
// priority's low bit from the special priority bit (mode 1) or from
// it and a special function code match on the dot color (mode 2,
// palette formats only, Table 11.2). The screen's color calculation
// enable is a precondition for every special color calculation mode
// (Table 12.3); mode 1 adds the special CC bit, mode 2 the bit and a
// code match (palette formats only), mode 3 the CRAM color's MSB for
// palette formats. A priority of 0 is transparent (Sec 11.1).
func specialPixel(s *ScreenParams, rgb uint32, transp bool, dotColor uint32, specialPri, specialCC, cramCC bool) uint32 {
	if transp {
		return 0
	}
	priority := s.Priority
	switch s.SFPRMD {
	case 1:
		priority &^= 1
		if specialPri {
			priority |= 1
		}
	case 2:
		if s.ColorMode < 3 {
			priority &^= 1
			if specialPri && SFCodeMatches(s.SFCode, dotColor) {
				priority |= 1
			}
		}
	}
	if priority == 0 {
		return 0
	}
	screenCC := s.ScreenCC
	cc := false
	switch s.SFCCMD {
	case 0:
		cc = screenCC
	case 1:
		cc = screenCC && specialCC
	case 2:
		if s.ColorMode < 3 {
			cc = screenCC && specialCC && SFCodeMatches(s.SFCode, dotColor)
		}
	case 3:
		if s.ColorMode >= 3 {
			cc = screenCC
		} else {
			cc = screenCC && cramCC
		}
	}
	px := priority<<24 | rgb
	if cc {
		px |= LayerCCBit
	}
	return px
}
