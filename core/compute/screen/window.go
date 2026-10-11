// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// WindowParams carries the window registers and the display state
// they are interpreted under (VDP2 manual Sec 8.1). Each of the two
// windows has a start and end X and Y register and a line window
// table address; W1 uses index 1.
type WindowParams struct {
	WPSX, WPSY, WPEX, WPEY [2]uint32
	LWTAU, LWTAL           [2]uint32
	HiRes                  bool   // 640 or 704 dots: X registers hold H9:H0
	LSMD3                  bool   // double-density interlace
	Field                  uint32 // field being rendered under LSMD3
	Intl2                  bool   // single-density interlace: per-line tables are indexed by y/2
}

// WindowX converts a raw window horizontal coordinate (WPSX/WPEX) to
// a pixel coordinate per Table 8.1: hi-res uses bits 9:0, normal
// resolution bits 9:1.
func WindowX(p WindowParams, raw uint32) int32 {
	if p.HiRes {
		return int32(raw & 0x03FF)
	}
	return int32(raw&0x03FE) >> 1
}

// LineWindowX converts a line window table word to a pixel
// coordinate. The words are signed 16-bit values: games exclude a
// line by storing start > end (an end of 0xFFFF reads as -1) and cover
// a full line with a negative start. In normal resolution bit 0 is
// invalid, so the signed word shifts right arithmetically by one.
func LineWindowX(p WindowParams, raw uint32) int32 {
	v := int32(int16(raw))
	if p.HiRes {
		return v
	}
	return v >> 1
}

// WindowY converts a raw window vertical coordinate (WPSY/WPEY) to a
// displayed-line value per Table 8.2. In double-density interlace the
// least significant bit is invalid (the value is V7:V0 in bits 8:1),
// so the boundary aligns to an even displayed line; otherwise all 9
// bits are valid.
func WindowY(p WindowParams, raw uint32) int32 {
	if p.LSMD3 {
		return int32(raw & 0x01FE)
	}
	return int32(raw & 0x01FF)
}

// LineTableY returns the per-line table index for field-line y:
// single-density interlace halves y since each entry covers two field
// lines, double-density maps y to the displayed line 2*y + field.
func LineTableY(p WindowParams, y int32) int32 {
	if p.LSMD3 {
		return y*2 + int32(p.Field)
	}
	if p.Intl2 {
		return y / 2
	}
	return y
}

// DisplayY returns the displayed line for field-line y.
func DisplayY(p WindowParams, y int32) int32 {
	if p.LSMD3 {
		return y*2 + int32(p.Field)
	}
	return y
}

// LineWindow is W0 or W1 resolved for a row: whether the row lies in
// the window's vertical range with a non-empty horizontal range, and
// that range in pixels, inclusive.
type LineWindow struct {
	On     bool
	SX, EX int32
}

// ResolveLineWindow resolves window w (0 or 1) for field-line y. The
// vertical range comes from WPSY/WPEY in displayed-line space. The
// horizontal range comes from the row's line window table entry when
// LWTAU bit 15 enables the line window, else from WPSX/WPEX. A start
// past the end on either axis, or a row outside the vertical range,
// leaves the window off for the row.
func ResolveLineWindow(vram *compute.VDP2VRAM, p WindowParams, y int32, w uint32) LineWindow {
	var lw LineWindow
	w &= 1
	sy := WindowY(p, p.WPSY[w])
	ey := WindowY(p, p.WPEY[w])
	dy := DisplayY(p, y)
	if sy > ey || dy < sy || dy > ey {
		return lw
	}
	var sx, ex int32
	if p.LWTAU[w]&0x8000 != 0 {
		addr := ((p.LWTAU[w] & 0x07) << 16) | (p.LWTAL[w] & 0xFFFE)
		entry := addr*2 + uint32(LineTableY(p, y))*4
		sx = LineWindowX(p, uint32(vram.U16BE(entry)))
		ex = LineWindowX(p, uint32(vram.U16BE(entry+2)))
	} else {
		sx = WindowX(p, p.WPSX[w])
		ex = WindowX(p, p.WPEX[w])
	}
	if sx > ex {
		return lw
	}
	lw.On = true
	lw.SX = sx
	lw.EX = ex
	return lw
}

// WindowRun is a horizontal run [X0, X1) of a row over which the W0
// and W1 inside states are constant.
type WindowRun struct {
	X0, X1 int32
	W0, W1 bool // the run lies inside the window
}

// WindowRuns is a row split into runs at the window edges: at most
// five, since two windows contribute at most four edges.
type WindowRuns struct {
	N    int32
	Runs [5]WindowRun
}

// inside reports whether x lies in the window's row range.
func (lw LineWindow) inside(x int32) bool {
	return lw.On && x >= lw.SX && x <= lw.EX
}

// windowCuts is the sorted set of run boundaries inside a row.
type windowCuts struct {
	n int32
	x [4]int32
}

// add inserts a run boundary at x if it lies strictly inside the row
// and is not already present.
func (c *windowCuts) add(x, width int32) {
	if x <= 0 || x >= width {
		return
	}
	i := c.n
	for i > 0 && c.x[i-1] >= x {
		if c.x[i-1] == x {
			return
		}
		c.x[i] = c.x[i-1]
		i--
	}
	c.x[i] = x
	c.n++
}

// BuildWindowRuns splits a row of the given width into runs at the
// edges of the two resolved windows.
func BuildWindowRuns(w0, w1 LineWindow, width int32, out *WindowRuns) {
	if !w0.On && !w1.On {
		out.N = 1
		out.Runs[0] = WindowRun{X0: 0, X1: width}
		return
	}
	var cuts windowCuts
	if w0.On {
		cuts.add(w0.SX, width)
		cuts.add(w0.EX+1, width)
	}
	if w1.On {
		cuts.add(w1.SX, width)
		cuts.add(w1.EX+1, width)
	}
	out.N = 0
	var x0 int32
	for i := int32(0); i <= cuts.n; i++ {
		x1 := width
		if i < cuts.n {
			x1 = cuts.x[i]
		}
		out.Runs[out.N] = WindowRun{X0: x0, X1: x1, W0: w0.inside(x0), W1: w1.inside(x0)}
		out.N++
		x0 = x1
	}
}

// WindowState is one window control byte evaluated over one run.
// Partial is the combination of the enabled W0 and W1 terms, or the
// operator's identity when neither is enabled (with every enable off
// the active area is the whole screen under AND and nothing under OR,
// Sec 8.1), and the sprite window flags say how the per-pixel sprite
// term joins it.
type WindowState struct {
	Partial  bool
	SWEn     bool
	SWArea   bool
	LogicAnd bool
}

// WindowRunState evaluates control byte wctl (Sec 8.1 layout:
// W0A=0x01, W0E=0x02, W1A=0x04, W1E=0x08, SWA=0x10, SWE=0x20,
// LOG=0x80; area bits 0=inside, 1=outside; LOG 0=OR, 1=AND) over a
// run where the W0 and W1 inside states are w0In and w1In.
func WindowRunState(wctl uint32, w0In, w1In bool) WindowState {
	logicAnd := wctl&0x80 != 0
	partial := logicAnd
	if wctl&0x02 != 0 {
		a := w0In != (wctl&0x01 != 0)
		if logicAnd {
			partial = partial && a
		} else {
			partial = partial || a
		}
	}
	if wctl&0x08 != 0 {
		a := w1In != (wctl&0x04 != 0)
		if logicAnd {
			partial = partial && a
		} else {
			partial = partial || a
		}
	}
	return WindowState{Partial: partial, SWEn: wctl&0x20 != 0, SWArea: wctl&0x10 != 0, LogicAnd: logicAnd}
}

// Active returns whether a pixel whose sprite window bit is swInside
// lies in the window's active area. For a layer window the active
// area is the masked area; for the color calculation window it is
// where color calculation is suppressed (Sec 8.2).
func (m WindowState) Active(swInside bool) bool {
	if !m.SWEn {
		return m.Partial
	}
	a := swInside != m.SWArea
	if m.LogicAnd {
		return m.Partial && a
	}
	return m.Partial || a
}
