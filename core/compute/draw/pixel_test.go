// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import (
	"bytes"
	"testing"

	"github.com/user-none/erings/core/compute"
)

type admitsCase struct {
	name string
	p    PixelParams
	x, y int32
	want bool
}

// TestAdmits checks each gate of the pixel write: the system clip,
// the user clip inside and outside modes (the bounds exclusive, the
// walk having widened mode 3's rectangle), mesh, and the double
// interlace parity.
func TestAdmits(t *testing.T) {
	base := PixelParams{ClipX: 319, ClipY: 223, FBWidth: 512, UCX1: 10, UCY1: 10, UCX2: 20, UCY2: 20}
	in := base
	in.UserClip = 2
	out := base
	out.UserClip = 3
	out.UCX1, out.UCY1, out.UCX2, out.UCY2 = 9, 9, 21, 21
	mesh := base
	mesh.Mesh = 1
	die := base
	die.DIE, die.ClipY = 1, 447
	dil := die
	dil.DILOdd = 1
	cases := []admitsCase{
		{"inside", base, 5, 5, true},
		{"left of the clip", base, -1, 5, false},
		{"past the clip x", base, 320, 5, false},
		{"above the clip", base, 5, -1, false},
		{"past the clip y", base, 5, 224, false},
		{"on the clip corner", base, 319, 223, true},
		{"user clip inside, interior", in, 15, 15, true},
		{"user clip inside, on the edge", in, 10, 15, false},
		{"user clip inside, outside", in, 5, 5, false},
		{"user clip outside, interior", out, 15, 15, false},
		{"user clip outside, on the edge", out, 10, 15, false},
		{"user clip outside, outside", out, 5, 5, true},
		{"user clip outside, just outside", out, 21, 15, true},
		{"mesh even", mesh, 4, 6, true},
		{"mesh odd", mesh, 4, 7, false},
		{"DIE even field, even y", die, 4, 10, true},
		{"DIE even field, odd y", die, 4, 11, false},
		{"DIE odd field, odd y", dil, 4, 11, true},
		{"DIE odd field, even y", dil, 4, 10, false},
		{"DIE doubled clip", dil, 4, 447, true},
	}
	for _, c := range cases {
		if got := Admits(&c.p, c.x, c.y); got != c.want {
			t.Errorf("%s: (%d,%d) admitted %v, want %v", c.name, c.x, c.y, got, c.want)
		}
	}
}

// TestAdmitsScaled checks the mesh and double interlace parities at
// scale: every sub-dot of an N by N block takes the decision of its
// native dot, at x and y shifted right by the scale shift, so a mesh
// dot covers its whole block and a field's rows keep their parity.
func TestAdmitsScaled(t *testing.T) {
	for shift := uint32(1); shift <= 3; shift++ {
		for _, mode := range []struct {
			name              string
			mesh, die, dilOdd uint32
		}{
			{"mesh", 1, 0, 0},
			{"DIE even field", 0, 1, 0},
			{"DIE odd field", 0, 1, 1},
			{"mesh and DIE odd field", 1, 1, 1},
		} {
			native := PixelParams{ClipX: 1 << 12, ClipY: 1 << 12, Mesh: mode.mesh, DIE: mode.die, DILOdd: mode.dilOdd}
			scaled := native
			scaled.ScaleShift = shift
			for y := int32(0); y < 32; y++ {
				for x := int32(0); x < 32; x++ {
					want := Admits(&native, x>>shift, y>>shift)
					if got := Admits(&scaled, x, y); got != want {
						t.Fatalf("scale shift %d %s: (%d,%d) admitted %v, want the native dot's %v", shift, mode.name, x, y, got, want)
					}
				}
			}
		}
	}
}

// TestCombine checks the value rule per mode: replace, MSB-on,
// shadow with and without a destination MSB, half luminance, half
// transparency with and without a destination MSB, the Gouraud
// correction with clamping, and an 8-bit frame buffer passing the
// pixel through to the byte store.
func TestCombine(t *testing.T) {
	const (
		src = 0x0C63 // r 3, g 3, b 3
		lit = 0xFFFF // MSB set, every channel 31
		dim = 0x7FFF // MSB clear, every channel 31
	)
	cases := []struct {
		name       string
		p          PixelParams
		dst, pixel uint32
		gouraud    uint32
		want       uint32
		readsDst   bool
	}{
		{"replace", PixelParams{}, lit, src, 0, src, false},
		{"replace keeps the source MSB", PixelParams{}, 0, src | 0x8000, 0, src | 0x8000, false},
		{"MSB on", PixelParams{MSBOn: 1}, 0, src, 0, src | 0x8000, false},
		{"shadow, destination lit", PixelParams{CC: 1}, lit, src, 0, 0x8000 | 15<<10 | 15<<5 | 15, true},
		{"shadow, destination dim", PixelParams{CC: 1}, dim, src, 0, NoStore, true},
		{"shadow with Gouraud ignores the source", PixelParams{CC: 5}, lit, src, 0x7FFF, 0x8000 | 15<<10 | 15<<5 | 15, true},
		{"half luminance", PixelParams{CC: 2}, 0, lit, 0, 0x8000 | 15<<10 | 15<<5 | 15, false},
		{"half transparent, destination lit", PixelParams{CC: 3}, lit, src, 0, 17<<10 | 17<<5 | 17, true},
		{"half transparent, destination dim", PixelParams{CC: 3}, dim, src, 0, src, true},
		{"Gouraud replace, neutral", PixelParams{CC: 4}, 0, src, 0x4210, src, false},
		{"Gouraud replace, clamped high", PixelParams{CC: 4}, 0, src, 0x7FFF, 18<<10 | 18<<5 | 18, false},
		{"Gouraud replace, clamped low", PixelParams{CC: 4}, 0, src, 0, 0, false},
		{"Gouraud then half luminance", PixelParams{CC: 6}, 0, src, 0x7FFF, 9<<10 | 9<<5 | 9, false},
		{"Gouraud then half transparent", PixelParams{CC: 7}, lit, src, 0x7FFF, 24<<10 | 24<<5 | 24, true},
		{"8-bit passes the pixel through", PixelParams{Is8bpp: true}, 0, 0x1234, 0, 0x1234, false},
	}
	for _, c := range cases {
		if got := Combine(&c.p, c.dst, c.pixel, c.gouraud); got != c.want {
			t.Errorf("%s: %#x, want %#x", c.name, got, c.want)
		}
		if reads := ReadsDestination(&c.p); reads != c.readsDst {
			t.Errorf("%s: reads destination %v, want %v", c.name, reads, c.readsDst)
		}
	}
}

// TestPutDotComposes checks PutDot against the gate, the read, the
// combine, and the store composed by hand, over every mode with a
// lit and a dim destination, a rejected position, and the two frame
// buffer widths.
func TestPutDotComposes(t *testing.T) {
	want, got := make(compute.VDP1FB, compute.VDP1FBSize), make(compute.VDP1FB, compute.VDP1FBSize)
	for cc := uint32(0); cc < 8; cc++ {
		for _, msb := range []uint32{0, 1} {
			for _, bpp8 := range []bool{false, true} {
				p := PixelParams{CC: cc, MSBOn: msb, Is8bpp: bpp8, ClipX: 319, ClipY: 223, FBWidth: 512}
				if bpp8 || msb != 0 {
					p.CC = 0
				}
				if bpp8 {
					p.MSBOn = 0
				}
				for i, dst := range []uint32{0x7FFF, 0xFFFF, 0} {
					x, y := int32(10+i), int32(20)
					index := uint32(y*p.FBWidth + x)
					want.PutU16BE(index*2, uint16(dst))
					got.PutU16BE(index*2, uint16(dst))
					PutDot(got, nil, &p, x, y, 0x0C63, 0x7FFF, 0)
					d := uint32(0)
					if ReadsDestination(&p) {
						d = uint32(want.U16BE(index * 2))
					}
					if out := Combine(&p, d, 0x0C63, 0x7FFF); out != NoStore {
						if p.Is8bpp {
							want.PutU8(index, uint8(out))
						} else {
							want.PutU16BE(index*2, uint16(out))
						}
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("cc %d msb %d 8bpp %t dst %#04x: PutDot differs from the composition", cc, msb, bpp8, dst)
					}
				}
				PutDot(got, nil, &p, 320, 20, 0x0C63, 0, 0)
				if !bytes.Equal(got, want) {
					t.Fatalf("cc %d: a rejected dot stored", cc)
				}
			}
		}
	}
}
