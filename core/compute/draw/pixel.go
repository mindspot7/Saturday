// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// The pixel write's passes. The walk stores every dot as it draws
// it (PassStore). A device draws a batch of ops in two passes over
// the same dots with no order between its threads: the tag pass
// (PassTag) raises each dot's pixel key word to the dot's draw order
// key, so the word ends at the key of the batch's last dot on the
// pixel, and the write pass (PassWrite) stores only the dot whose
// key the word holds. The two passes give the pixel the batch's last
// replace-class dot, which is the walk's result on a pixel whose
// dots replace.
const (
	PassStore = 0
	PassTag   = 1
	PassWrite = 2
)

// PixelParams is the per-command state of the pixel write: the color
// calculation mode and draw mode bits, the clip rectangles, the
// frame buffer geometry, and the pass. The caller resolves the frame
// buffer to draw into (under DIE=1 with DIL=1 the odd-field buffer)
// and, for 8-bit frame buffers and MSB-on, sets CC to 0 since those
// modes replace.
type PixelParams struct {
	CC         uint32 // CMDPMOD bits 2:0: 0 replace, 1 shadow, 2 half luminance, 3 half transparent, 4-7 with Gouraud
	MSBOn      uint32 // CMDPMOD MON, 1 to set the pixel MSB: a bit the pixel write shifts into place
	Mesh       uint32 // CMDPMOD MESH, 1 to draw only where x+y is even: a bit Admits masks with
	UserClip   uint32 // CMDPMOD bits 10:9: 2 draws inside the user clip only, 3 outside only
	UCX1       int32  // user clip rectangle, exclusive bounds: mode 2 draws strictly inside, mode 3 strictly outside (the walk widens its rectangle by one)
	UCY1       int32
	UCX2       int32
	UCY2       int32
	ClipX      int32 // system clip, inclusive, clamped to the frame buffer
	ClipY      int32
	DIE        uint32 // 1 for double interlace, y spanning both fields with DIL selecting the parity drawn: a bit Admits masks with
	DILOdd     uint32 // 1 for the odd parity under DIE: a bit Admits compares
	Is8bpp     bool
	FBWidth    int32
	FBHeight   int32  // rows of the frame buffer the op draws into
	ScaleShift uint32 // log2 of the render scale N (N = 1 << ScaleShift), 0 at native: positions are in scaled dots, and the parities and the DIE row are taken on the native dot
	Pass       uint32 // PassStore, PassTag, or PassWrite
}

// YieldChunk is the dots a span function draws between checks of its
// cycle budget: it returns at the first chunk boundary at or past the
// budget. Smaller values give finer SH-2 / VDP1 interleaving at the
// cost of more in-loop branch overhead; larger values reduce overhead
// but allow more system cycles of overshoot per yield. Must be at
// least 1.
const YieldChunk = 8

// UnboundedCycles is a budget no line reaches.
const UnboundedCycles = 0x7FFFFFFF

// Admits is the pixel write's gate: whether a dot at (x, y), in the
// command's coordinates (logical rows under DIE), lands. It lands
// inside the system clip, inside the user clip in user clip mode 2
// and outside it in mode 3 (userClipAdmits), where x+y is even under
// mesh, and on the drawn field's parity under double interlace. The
// mesh and the parity are taken on the native dot, the position
// shifted right by ScaleShift, so a sub-dot takes its block's
// decision. The answer depends on the position and the params alone,
// so a pixel's dots all share it. The checks are written to inline
// into the per-dot store: the clip compares unsigned, so a negative
// coordinate fails as a large one (the clip bounds are never
// negative); mesh and the parity are masks, Mesh, DIE, and DILOdd
// being 0 or 1.
func Admits(p *PixelParams, x, y int32) bool {
	return uint32(x) <= uint32(p.ClipX) && uint32(y) <= uint32(p.ClipY) &&
		(p.UserClip < 2 || userClipAdmits(p, x, y)) &&
		(p.Mesh&(uint32(x^y)>>p.ScaleShift)|p.DIE&(uint32(y)>>p.ScaleShift^p.DILOdd))&1 == 0
}

// userClipAdmits is the user clip's part of the gate: whether the
// dot is strictly inside the bounds, which mode 2 admits and mode 3
// rejects.
func userClipAdmits(p *PixelParams, x, y int32) bool {
	return (x > p.UCX1 && x < p.UCX2 && y > p.UCY1 && y < p.UCY2) == (p.UserClip == 2)
}

// ReadsDestination reports whether the combine reads the pixel
// already in the frame buffer: shadow and half transparency, the odd
// color calculation modes.
func ReadsDestination(p *PixelParams) bool {
	return p.CC&1 != 0
}

// NoStore is the combine's result when the dot stores nothing: a
// value above any 16-bit pixel.
const NoStore = 1 << 16

// ApplyColorCalc applies a color calculation mode other than replace
// to a source pixel against the destination pixel dst, returning the
// value to store or NoStore. Shadow (modes 1 and 5) halves the
// destination where its MSB is set and stores nothing otherwise, the
// source and Gouraud unused. The other modes apply the Gouraud
// correction for modes 4 to 7, then half luminance or half
// transparency. It is one call for the non-replace modes, so that
// Combine inlines into the per-dot store.
func ApplyColorCalc(cc, dst, pixel, gouraud uint32) uint32 {
	if cc&3 == 1 {
		if dst&0x8000 == 0 {
			return NoStore
		}
		dr := (dst & 0x1F) >> 1
		dg := ((dst >> 5) & 0x1F) >> 1
		db := ((dst >> 10) & 0x1F) >> 1
		return (dst & 0x8000) | db<<10 | dg<<5 | dr
	}
	if cc >= 4 {
		sr := int32(pixel&0x1F) + int32(gouraud&0x1F) - 0x10
		sg := int32((pixel>>5)&0x1F) + int32((gouraud>>5)&0x1F) - 0x10
		sb := int32((pixel>>10)&0x1F) + int32((gouraud>>10)&0x1F) - 0x10
		pixel = (pixel & 0x8000) | clamp5(sb)<<10 | clamp5(sg)<<5 | clamp5(sr)
	}
	switch cc & 3 {
	case 2:
		sr := (pixel & 0x1F) >> 1
		sg := ((pixel >> 5) & 0x1F) >> 1
		sb := ((pixel >> 10) & 0x1F) >> 1
		return (pixel & 0x8000) | sb<<10 | sg<<5 | sr
	case 3:
		if dst&0x8000 == 0 {
			return pixel
		}
		sr := ((pixel & 0x1F) + (dst & 0x1F)) >> 1
		sg := (((pixel >> 5) & 0x1F) + ((dst >> 5) & 0x1F)) >> 1
		sb := (((pixel >> 10) & 0x1F) + ((dst >> 10) & 0x1F)) >> 1
		return (pixel & 0x8000) | sb<<10 | sg<<5 | sr
	}
	return pixel
}

// Combine is the pixel write's value rule: from the destination
// pixel dst (meaningful only when ReadsDestination), the source
// pixel, and the Gouraud value, the value to store, or NoStore.
// Replace stores the source with bit 15 set under MSB-on (MSBOn is
// 0 or 1; the walk forces replace mode for MSB-on); the other modes
// go through ApplyColorCalc. An 8-bit frame buffer's store keeps
// the low byte: the walk forces replace mode there, so the width is
// the store's, not the rule's.
func Combine(p *PixelParams, dst, pixel, gouraud uint32) uint32 {
	if p.CC != 0 {
		return ApplyColorCalc(p.CC, dst, pixel, gouraud)
	}
	return pixel | p.MSBOn<<15
}

// PutDot writes the dot at (x, y): Admits decides whether it lands,
// the physical row is y halved under DIE, and then the op's pass
// decides what the dot does, one test per dot. The store pass reads
// the destination when the mode reads it, Combine gives the value,
// and the store is a byte in an 8-bit frame buffer and a word
// otherwise. The tag and write passes go through putDotKeyed with
// the dot's draw order key (LineKey); the store pass reads neither
// the key nor the round state.
//
// Under DIE=1 the game submits y in 0..2*height-1 (VDP1 manual Table
// 1.2): the DIL parity filter discards the opposite parity and the
// surviving y halves to the physical row.
func PutDot(fb compute.VDP1FB, rs *compute.RoundState, p *PixelParams, x, y int32, pixel, gouraud, key uint32) {
	if p.Pass != PassStore {
		putDotKeyed(fb, rs, p, x, y, pixel, gouraud, key)
		return
	}
	if !Admits(p, x, y) {
		return
	}
	index := uint32(PhysicalRow(p, y)*p.FBWidth + x)
	dst := uint32(0)
	if ReadsDestination(p) {
		dst = uint32(fb.U16BE(index * 2))
	}
	out := Combine(p, dst, pixel, gouraud)
	if out == NoStore {
		return
	}
	if p.Is8bpp {
		fb.PutU8(index, uint8(out))
		return
	}
	fb.PutU16BE(index*2, uint16(out))
}

// putDotKeyed is the pixel write of the tag and write passes: Admits
// and the physical index as in the store pass. A dot that reads the
// destination is appended to its pixel's fragment list in the tag
// pass, for the round's resolve to apply in order, and is nothing
// in the write pass. For a replace-class dot the tag pass raises
// the pixel's order stamp to the dot's key, and the write pass
// stores the dot as the store pass would when the stamp holds its
// key.
func putDotKeyed(fb compute.VDP1FB, rs *compute.RoundState, p *PixelParams, x, y int32, pixel, gouraud, key uint32) {
	if !Admits(p, x, y) {
		return
	}
	index := uint32(PhysicalRow(p, y)*p.FBWidth + x)
	if ReadsDestination(p) {
		if p.Pass == PassTag {
			appendFragment(rs, index, key, pixel, gouraud, p.CC)
		}
		return
	}
	if p.Pass == PassTag {
		rs.Stamps.Raise(index, key)
		return
	}
	if rs.Stamps[index] != key {
		return
	}
	dst := uint32(0)
	if ReadsDestination(p) {
		dst = uint32(fb.U16BE(index * 2))
	}
	out := Combine(p, dst, pixel, gouraud)
	if out == NoStore {
		return
	}
	if p.Is8bpp {
		fb.PutU8(index, uint8(out))
		return
	}
	fb.PutU16BE(index*2, uint16(out))
}
