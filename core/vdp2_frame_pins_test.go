package core

import (
	"fmt"
	"strings"
	"testing"
)

// goldenBandRows is the number of output rows hashed per band. A band
// hash mismatch localizes a change to 8 lines and the failure prints
// the band's first row so the change is diagnosable.
const goldenBandRows = 8

// fnv1a32 hashes bytes with FNV-1a.
func fnv1a32(h uint32, data []byte) uint32 {
	for _, b := range data {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
}

// vdp2BandHashes hashes the composited output in bands of goldenBandRows
// rows (RGB bytes only).
func vdp2BandHashes(v *VDP2, swr *softwareRenderer) []uint32 {
	w := int(v.activeWidth)
	h := int(v.frame.height)
	if v.frame.lsmd3 {
		h *= 2
	}
	fb := swr.framebuffer()
	var out []uint32
	for y0 := 0; y0 < h; y0 += goldenBandRows {
		hash := uint32(2166136261)
		for y := y0; y < y0+goldenBandRows && y < h; y++ {
			row := fb[y*w*4 : (y+1)*w*4]
			for x := 0; x < w; x++ {
				hash = fnv1a32(hash, row[x*4:x*4+3])
			}
		}
		out = append(out, hash)
	}
	return out
}

// formatHashes renders band hashes as a Go literal for pasting into the
// expected tables.
func formatHashes(h []uint32) string {
	parts := make([]string, len(h))
	for i, x := range h {
		parts[i] = fmt.Sprintf("0x%08X", x)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// checkBands compares band hashes with the expected table. With no
// expected table the test fails and prints the literal to record.
func checkBands(t *testing.T, name string, got, want []uint32, describeBand func(band int) string) {
	t.Helper()
	if want == nil {
		t.Errorf("%s: no expected band hashes recorded; current: %s", name, formatHashes(got))
		return
	}
	if len(got) != len(want) {
		t.Errorf("%s: %d bands, want %d", name, len(got), len(want))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: band %d (rows %d-%d) hash 0x%08X, want 0x%08X\n%s",
				name, i, i*goldenBandRows, i*goldenBandRows+goldenBandRows-1, got[i], want[i], describeBand(i))
		}
	}
}

// describeVDP2Band returns the first row of the band as run-length
// encoded RGB triples.
func describeVDP2Band(v *VDP2, swr *softwareRenderer) func(band int) string {
	return func(band int) string {
		w := int(v.activeWidth)
		y := band * goldenBandRows
		var sb strings.Builder
		fmt.Fprintf(&sb, "row %d: ", y)
		run := 0
		var pr, pg, pb uint8
		flush := func() {
			if run > 0 {
				fmt.Fprintf(&sb, "%dx(%d,%d,%d) ", run, pr, pg, pb)
			}
		}
		for x := 0; x < w; x++ {
			r, g, b := outRGB(v, swr, x, y)
			if run > 0 && r == pr && g == pg && b == pb {
				run++
				continue
			}
			flush()
			pr, pg, pb, run = r, g, b, 1
		}
		flush()
		return sb.String()
	}
}

// goldenSixLayers is the six-layer interaction scene at 320x224.
func goldenSixLayers(t *testing.T) (*VDP2, *softwareRenderer) {
	v, fb := setupSixLayerScene(t)
	swr := newSoftwareRenderer(v)
	renderTestFrameFB(v, swr, fb)
	return v, swr
}

// goldenRBG0WindowCoefficient is RBG0 with the rotation parameter window
// (RPMD 3), parameter B on the inside of W0, a coefficient table with
// line color enable on parameter A, line color insertion, and color
// calculation.
func goldenRBG0WindowCoefficient(t *testing.T) (*VDP2, *softwareRenderer) {
	v := setupRBG0ParamAB(t)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2RPMD] = 0x0003
	v.regs[vdp2WPSX0], v.regs[vdp2WPEX0] = 0, 40
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 0, 100
	v.regs[vdp2WCTLD] = 0x0002
	v.regs[vdp2KTCTL] = 0x0011
	writeRotParam32(v, 0x10000, 0x54, 0x6000, 0x0000)
	writeRotParam32(v, 0x10000, 0x58, 0x0001, 0x0000) // dKAst 1.0: one entry per line
	for line := uint32(0); line < 224; line++ {
		w0 := uint16(0x2501)
		if line%16 >= 12 {
			w0 = 0x8501 // transparent lines
		}
		writeVRAM16(v, 0x18000+line*4, w0)
		writeVRAM16(v, 0x18002+line*4, 0x0000)
	}
	v.regs[vdp2LNCLEN] = 1 << 4
	v.regs[vdp2LCTAU] = 0x0002
	v.regs[vdp2LCTAL] = 0x8000
	writeVRAM16(v, 0x50000, 0x0180)
	writeCRAM16Test(v, 0x1A5, 0x7C00)
	v.regs[vdp2CCCTL] = 1 << 4
	v.regs[vdp2CCRR] = 16
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0x83E0)
	renderTestFrame(v, swr)
	return v, swr
}

// goldenRBG1OverRBG0 is RBG1 with a coefficient of 2.0 over RBG0 on the
// same page.
func goldenRBG1OverRBG0(t *testing.T) (*VDP2, *softwareRenderer) {
	v := setupRBG1Scene(t)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2KTCTL] = 0x0100
	writeRotParam32(v, 0x10080, 0x54, 0x6000, 0x0000)
	writeRotParam32(v, 0x10080, 0x58, 0x0000, 0x0000)
	writeVRAM16(v, 0x18000, 0x0002)
	writeVRAM16(v, 0x18002, 0x0000)
	writeRotParam32(v, 0x10080, 0x00, 8, 0x0000) // Xst_B = 8
	renderTestFrame(v, swr)
	return v, swr
}

// goldenHiResSpriteWindowCC is a 640-dot frame with NBG0 color
// calculating against a blue back screen and masked by the sprite
// window, enabled by SPWINEN on sprite type 2.
func goldenHiResSpriteWindowCC(t *testing.T) (*VDP2, *softwareRenderer) {
	v := setupNBG0FullTile(t)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2TVMD] = 0x8002
	v.recalcTiming()
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0xFC00)
	v.regs[vdp2SPCTL] = 0x0012
	v.regs[vdp2WCTLA] = 0x0020
	v.regs[vdp2CCCTL] = 0x0001
	v.regs[vdp2CCRNA] = 16
	data := make([]byte, 512*256*2)
	for y := 0; y < 8; y++ {
		off := (y*512 + 1) * 2
		data[off] = 0x80
	}
	renderTestFrameFB(v, swr, vdp1FBView{data: data, width: 512, height: 256})
	return v, swr
}

// goldenLSMD3LineScroll is a double-density interlace frame (both fields
// rendered) of NBG0 with a per-displayed-line horizontal scroll table.
func goldenLSMD3LineScroll(t *testing.T) (*VDP2, *softwareRenderer) {
	v := setupNBG0LineScrollScene(t)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2SCRCTL] = 0x0002
	for line := uint32(0); line < 448; line++ {
		writeLineScrollEntry(v, 0x20000+line*4, uint16(line%8), 0)
	}
	setLSMD3(v, false)
	swr.beginFrame() // the frame start where the geometry changed
	renderTestFrame(v, swr)
	setLSMD3(v, true)
	renderTestFrame(v, swr)
	return v, swr
}

// goldenSixLayers352 is the six-layer scene at 352 dots.
func goldenSixLayers352(t *testing.T) (*VDP2, *softwareRenderer) {
	v, fb := setupSixLayerScene(t)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2TVMD] = 0x8001
	v.recalcTiming()
	renderTestFrameFB(v, swr, fb)
	return v, swr
}

// goldenSixLayersPAL256 is the six-layer scene on a PAL 256-line frame.
func goldenSixLayersPAL256(t *testing.T) (*VDP2, *softwareRenderer) {
	v, fb := setupSixLayerScene(t)
	swr := newSoftwareRenderer(v)
	v.SetPAL(true)
	v.regs[vdp2TVMD] = 0x8020
	v.recalcTiming()
	renderTestFrameFB(v, swr, fb)
	return v, swr
}

// goldenFullFrame is a scene with content on every row: an NBG0 gradient
// bitmap (priority 4) masked by W0 on the left half, a rotated RBG0
// gradient bitmap (priority 3) color-calculating against an EXBG
// position-encoded frame (priority 2), and a sprite dot pattern
// (priority 5).
func goldenFullFrame(t *testing.T) (*VDP2, *softwareRenderer) {
	v := newTestVDP2()
	swr := newSoftwareRenderer(v)
	v.regs[vdp2BGON] = 0x0001 | 1<<4
	// NBG0: 256-color bitmap 512x256, dot = (x*3 + y) & 0xFF.
	v.regs[vdp2CHCTLA] = 0x0012
	v.regs[vdp2BMPNA] = 0x0000
	v.regs[vdp2MPOFN] = 0x0000
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			v.vram[y*512+x] = uint8(x*3 + y)
		}
	}
	for i := 1; i < 256; i++ {
		writeCRAM16Test(v, uint32(i), uint16(i*0x0421)&0x7FFF)
	}
	// RBG0: 256-color bitmap at map offset 2 (0x40000), dot = (x ^ y) & 0xFF,
	// rotated by 30 degrees (A = E = 0.866, B = -0.5, D = 0.5).
	v.regs[vdp2CHCTLB] = 0x1200
	v.regs[vdp2BMPNB] = 0x0000
	v.regs[vdp2MPOFR] = 0x0002
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			v.vram[0x40000+y*512+x] = uint8(x ^ y)
		}
	}
	v.regs[vdp2RPMD] = 0x0000
	v.regs[vdp2RPTAU] = 0x0000
	v.regs[vdp2RPTAL] = 0xA000
	p := uint32(0x14000)
	writeRotParam32(v, p, 0x10, 0x0001, 0x0000) // DYst = 1.0
	writeRotParam32(v, p, 0x14, 0x0001, 0x0000) // DX = 1.0
	writeRotParam32(v, p, 0x1C, 0x0000, 0xDDC0) // A = 0.866
	writeRotParam32(v, p, 0x20, 0x000F, 0x8000) // B = -0.5
	writeRotParam32(v, p, 0x28, 0x0000, 0x8000) // D = 0.5
	writeRotParam32(v, p, 0x2C, 0x0000, 0xDDC0) // E = 0.866
	writeRotParam32(v, p, 0x4C, 0x0001, 0x0000) // kx = 1.0
	writeRotParam32(v, p, 0x50, 0x0001, 0x0000) // ky = 1.0
	// EXBG in the NBG1 slot, position-encoded frame.
	v.regs[vdp2EXTEN] = 0x0001
	v.SetEXBGSource(&fakeEXBGUnsized{rgb: exbgTestFrame(320, 224), w: 320, h: 224})
	// Priorities: sprite 5, NBG0 4, RBG0 3, EXBG 2.
	v.regs[vdp2PRINA] = 0x0204
	v.regs[vdp2PRIR] = 0x0003
	v.regs[vdp2SPCTL] = 0x0000
	v.regs[vdp2PRISA] = 0x0005
	writeCRAM16Test(v, 5, 0x03FF)
	writeCRAM16Test(v, 6, 0x7C1F)
	writeCRAM16Test(v, 7, 0x03E0)
	// W0 masks NBG0 on x 0..159.
	v.regs[vdp2WPSX0], v.regs[vdp2WPEX0] = 0, 318
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 0, 223
	v.regs[vdp2WCTLA] = 0x0002
	// RBG0 color calculation, ratio 16.
	v.regs[vdp2CCCTL] = 1 << 4
	v.regs[vdp2CCRR] = 16
	fb := vdp1FBView{data: make([]byte, 512*256*2), width: 512, height: 256}
	for y := 0; y < 224; y++ {
		for x := 0; x < 320; x++ {
			if (x+y)%5 == 0 {
				setFBPixel16(fb, x, y, 0x0005+uint16((x/3)%3))
			}
		}
	}
	renderTestFrameFB(v, swr, fb)
	return v, swr
}

// TestGoldenVDP2Scenes pins the software renderer's output on full-frame
// synthetic scenes as per-band hashes. A mismatch means the renderer's
// output changed; the pixel tests locate the cause, this test detects it.
func TestGoldenVDP2Scenes(t *testing.T) {
	// repeatHash returns the band table for a scene whose content sits in
	// band 0 and whose remaining bands are the uniform back screen.
	repeatHash := func(first, rest uint32, bands int) []uint32 {
		out := make([]uint32, bands)
		out[0] = first
		for i := 1; i < bands; i++ {
			out[i] = rest
		}
		return out
	}
	scenes := []struct {
		name  string
		build func(t *testing.T) (*VDP2, *softwareRenderer)
		want  []uint32
	}{
		{"six layers 320x224", goldenSixLayers, repeatHash(0x2F857895, 0x624075C5, 28)},
		{"RBG0 window coefficient line color", goldenRBG0WindowCoefficient, []uint32{
			0x76A8DE85, 0x6FFD3545, 0x018D4945, 0x6FFD3545, 0x018D4945, 0x6FFD3545, 0x018D4945,
			0x6FFD3545, 0x018D4945, 0x6FFD3545, 0x018D4945, 0x6FFD3545, 0xA5760B1C, 0x58E53E45,
			0x00813E45, 0x58E53E45, 0x00813E45, 0x58E53E45, 0x00813E45, 0x58E53E45, 0x00813E45,
			0x58E53E45, 0x00813E45, 0x58E53E45, 0x00813E45, 0x58E53E45, 0x00813E45, 0x58E53E45,
		}},
		{"RBG1 over RBG0", goldenRBG1OverRBG0, append([]uint32{0x2538A6E5, 0x08E13D65}, repeatHash(0x6CE75CC5, 0x6CE75CC5, 26)...)},
		{"hi-res sprite window CC", goldenHiResSpriteWindowCC, repeatHash(0xF9499B75, 0x9F363AC5, 28)},
		{"LSMD3 line scroll both fields", goldenLSMD3LineScroll, repeatHash(0x9DCF8325, 0x11A349C5, 56)},
		{"six layers 352 wide", goldenSixLayers352, repeatHash(0x32273095, 0xD58271C5, 28)},
		{"six layers PAL 256 lines", goldenSixLayersPAL256, repeatHash(0x2F857895, 0x624075C5, 32)},
		{"full frame", goldenFullFrame, []uint32{
			0x63C73B25, 0xCD9D541A, 0xFC2F5E5D, 0x0DA82156, 0xA3912F51, 0xDF54E2DF, 0x445AD104,
			0x79B0A1A6, 0x2B697124, 0x6D925D8A, 0xF933DE9D, 0x62C542FC, 0x42AD616D, 0xF51895D7,
			0xEDE846C0, 0xF1E7853E, 0xDB5D34B0, 0x4194F6C4, 0x7D8FE5CC, 0x583AF314, 0xC48B2F6A,
			0xEF7E6A3B, 0x2DA16221, 0xE750609E, 0xE22492CB, 0xFE17574E, 0xD13C23B3, 0x37D45916,
		}},
	}
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			v, swr := s.build(t)
			checkBands(t, s.name, vdp2BandHashes(v, swr), s.want, describeVDP2Band(v, swr))
		})
	}
}
