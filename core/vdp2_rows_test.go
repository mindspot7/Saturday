// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute/screen"
)

// rowScene returns the named benchmark scene's VDP2 and view.
func rowScene(t *testing.T, name string) (*VDP2, vdp1FBView) {
	t.Helper()
	for _, s := range benchVDP2Scenes() {
		if s.name == name {
			return s.build()
		}
	}
	t.Fatalf("no scene %q", name)
	return nil, vdp1FBView{}
}

// spanLayerValid reports whether a chain entry names a layer its
// entry can run over: an NBG entry one of the four NBG buffers, an
// RBG entry RBG0 or RBG1, the gradation any layer, the composite 0,
// and the external row the NBG1 buffer.
func spanLayerValid(entry, layer int) bool {
	switch entry {
	case entryNBGCell, entryNBGBitmap:
		return layer <= screen.LayerNBG3
	case entryRBGCell, entryRBGBitmap:
		return layer == screen.LayerRBG0 || layer == screen.LayerRBG1
	case entryGradation:
		return layer < layerCount
	case entryComposite:
		return layer == 0
	case entryExternal:
		return layer == screen.LayerNBG1
	}
	return false
}

// TestRowRecordsChain renders a six-layer frame row by row and checks
// each row's record as BeginLine built it: the geometry and a chain
// that begins with an NBG span, ends with the composite, and names
// only entries over layers they can run over.
func TestRowRecordsChain(t *testing.T) {
	v, fb := rowScene(t, "six layers")
	swr := newSoftwareRenderer(v)
	renderFrame(v, swr, fb, nil)
	v.BeginFrame()
	swr.beginFrame()
	lines := int(v.ActiveLines())
	for y := 0; y < lines; y++ {
		beginRow(v, swr, y, fb)
		r := v.rowRecord(y)
		if r.NSpans == 0 {
			t.Fatalf("row %d record has no chain", y)
		}
		first, _ := spanKind(r.Spans[0])
		last, _ := spanKind(r.Spans[r.NSpans-1])
		if (first != entryNBGCell && first != entryNBGBitmap) || last != entryComposite {
			t.Fatalf("row %d chain %v", y, r.Spans[:r.NSpans])
		}
		for i := 0; i < int(r.NSpans); i++ {
			if entry, layer := spanKind(r.Spans[i]); !spanLayerValid(entry, layer) {
				t.Fatalf("row %d span %d = %#x", y, i, r.Spans[i])
			}
		}
		swr.renderLine(y)
		v.EndLine()
	}
	v.EndFrame()
	got := 0
	for y := 0; y < lines; y++ {
		if v.rowRecord(y).NSpans > 0 {
			got++
		}
	}
	if got != lines {
		t.Errorf("recorded %d rows with a chain, want %d", got, lines)
	}
}

// TestRowRecordsRBG1AndInactiveRows checks that a scene using RBG1
// records its span and that rows outside the display are recorded
// as empty.
func TestRowRecordsRBG1AndInactiveRows(t *testing.T) {
	v, fb := rowScene(t, "RBG1 over RBG0")
	renderFrame(v, newSoftwareRenderer(v), fb, nil)
	v.BeginFrame()
	v.BeginLine(0, fb)
	r := v.rowRecord(0)
	found := false
	for i := 0; i < int(r.NSpans); i++ {
		if entry, layer := spanKind(r.Spans[i]); (entry == entryRBGCell || entry == entryRBGBitmap) && layer == screen.LayerRBG1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("RBG1 not recorded: spans %v", r.Spans)
	}
	lines := int(v.ActiveLines())
	v.BeginLine(lines+3, fb)
	if n := v.rowRecord(lines + 3).NSpans; n != 0 {
		t.Errorf("row past the display recorded %d spans", n)
	}
}

// TestRowRecordLayout checks the record is a fixed-stride,
// buffer-visible layout: every params struct sits at an 8-byte
// aligned offset and the record size is a multiple of 8, so an entry
// point can address row y's params as base + y*stride + offset.
func TestRowRecordLayout(t *testing.T) {
	var r lineRecord
	offsets := map[string]uintptr{
		"NBG":  unsafe.Offsetof(r.NBG),
		"RBG":  unsafe.Offsetof(r.RBG),
		"Comp": unsafe.Offsetof(r.Comp),
		"Grad": unsafe.Offsetof(r.Grad),
	}
	for name, off := range offsets {
		if off%8 != 0 {
			t.Errorf("%s at offset %d is not 8-byte aligned", name, off)
		}
	}
	if unsafe.Sizeof(r)%8 != 0 {
		t.Errorf("record size %d is not a multiple of 8", unsafe.Sizeof(r))
	}
	t.Logf("record %d bytes, NBG %d, RBG %d, composite %d", unsafe.Sizeof(r), unsafe.Sizeof(r.NBG[0]), unsafe.Sizeof(r.RBG[0]), unsafe.Sizeof(r.Comp))
}

// TestRowRecordsLineColorCleared checks that the software renderer's
// row setup point empties the line color row of exactly the rotation
// layers the row's chain renders, and leaves the buffer of a layer the
// chain does not name alone. The GPU renderer empties its device rows
// from the same words, so this is the one rule both renderers read.
func TestRowRecordsLineColorCleared(t *testing.T) {
	v, fb := rowScene(t, "RBG1 over RBG0")
	swr := newSoftwareRenderer(v)
	renderFrame(v, swr, fb, nil)
	v.BeginFrame()
	swr.beginFrame()
	width := int(v.ActiveWidth())
	for i := range swr.in.LineColors {
		for j := range swr.in.LineColors[i] {
			swr.in.LineColors[i][j] = 0xEE
		}
	}
	beginRow(v, swr, 0, fb)
	r := v.rowRecord(0)
	rotation := map[int]bool{}
	for i := 0; i < int(r.NSpans); i++ {
		if entry, layer := spanKind(r.Spans[i]); entry == entryRBGCell || entry == entryRBGBitmap {
			rotation[layer] = true
		}
	}
	if len(rotation) == 0 {
		t.Fatalf("no rotation span in the chain: %v", r.Spans[:r.NSpans])
	}
	for layer := screen.LayerRBG0; layer <= screen.LayerRBG1; layer++ {
		buf := swr.in.LineColors[layer-screen.LayerRBG0]
		cleared := true
		for _, b := range buf[:width] {
			cleared = cleared && b == 0
		}
		if cleared != rotation[layer] {
			t.Errorf("layer %d: row cleared %v, the chain renders it %v", layer, cleared, rotation[layer])
		}
		// The row beyond the display is never the row's.
		if buf[width] != 0xEE {
			t.Errorf("layer %d: the clear ran past the row", layer)
		}
	}
}

// TestRowRecordsGradationLayer checks the chain names the layer the
// gradation blurs for every BOKN selection: the row setup decides
// it once, and a renderer that binds by the chain's layer index
// blurs the buffer the software renderer blurs.
func TestRowRecordsGradationLayer(t *testing.T) {
	cases := []struct {
		name  string
		bokn  uint16
		layer int
	}{
		{"RBG0", 1, screen.LayerRBG0},
		{"NBG0", 2, screen.LayerNBG0},
		{"NBG1", 4, screen.LayerNBG1},
		{"NBG2", 5, screen.LayerNBG2},
		{"NBG3", 6, screen.LayerNBG3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, fb := rowScene(t, "six layers")
			benchReg(v, vdp2CCCTL, 0x8000|c.bokn<<12)
			renderFrame(v, newSoftwareRenderer(v), fb, nil)
			v.BeginFrame()
			v.BeginLine(0, fb)
			r := v.rowRecord(0)
			for i := 0; i < int(r.NSpans); i++ {
				if entry, layer := spanKind(r.Spans[i]); entry == entryGradation {
					if layer != c.layer {
						t.Fatalf("gradation over layer %d, want %d", layer, c.layer)
					}
					return
				}
			}
			t.Fatalf("no gradation span: %v", r.Spans[:r.NSpans])
		})
	}
}

// TestRowRecordsDISPOff renders a displayed frame and then a frame
// with DISP clear, and checks the records the second frame leaves:
// every row's chain is empty. A renderer that runs after the display
// reads the records to decide what to draw, so a record carried over
// from the last displayed frame would draw that frame's row.
func TestRowRecordsDISPOff(t *testing.T) {
	v, fb := rowScene(t, "six layers")
	swr := newSoftwareRenderer(v)
	renderFrame(v, swr, fb, nil)
	lines := int(v.ActiveLines())
	if lines == 0 || v.rowRecord(0).NSpans == 0 {
		t.Fatalf("displayed frame: %d lines, row 0 chain %d spans", lines, v.rowRecord(0).NSpans)
	}

	benchReg(v, vdp2TVMD, v.Read(uint32(vdp2TVMD)*2)&^0x8000)
	renderFrame(v, swr, fb, nil)
	if v.frame.disp {
		t.Fatalf("DISP still set after clearing TVMD bit 15")
	}
	for y := 0; y < lines; y++ {
		if n := v.rowRecord(y).NSpans; n != 0 {
			t.Errorf("row %d names %d spans with the display off, want 0", y, n)
		}
	}
}
