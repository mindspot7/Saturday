// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package msl

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

const parentPath = "example.com/compute"

const parentSrc = `package compute

const BufSize = 1024
const WordCount = 256

type Buf [BufSize]byte
type Words [WordCount]uint32
type Variable []uint32

func (b *Buf) U8(a uint32) uint8 { return b[a&(BufSize-1)] }

func (b *Buf) U16BE(a uint32) uint16 {
	return uint16(b[a&(BufSize-1)])<<8 | uint16(b[(a+1)&(BufSize-1)])
}

func (b *Buf) PutU8(a uint32, v uint8) { b[a&(BufSize-1)] = v }
`

const entrySrc = `package sub

import "example.com/compute"

var table = [4]uint32{1, 2, 4, 8}

type Inner struct {
	Scale int64
}

type Params struct {
	Base  uint32
	Ratio [2]int32
	Inner Inner
	On    bool
}

type State struct {
	Pos int32
}

type stepper struct {
	x, dx int32
}

func (s *stepper) step() { s.x += s.dx }

func two(a int32) (int32, bool) { return a + 1, a > 0 }

func narrow(a, b uint8, c int16) uint32 {
	d := a + b
	if int16(d)*c == -3 {
		return 1
	}
	return uint32(d&^b) | uint32(c)
}

func Entry(in *compute.Buf, out *compute.Words, p *Params, st *State, x uint32, y int32) {
	var s stepper = stepper{x: int32(x), dx: 1}
	s.step()
	v, ok := two(s.x)
	if ok && p.Ratio[v&1] > 0 || y == 2 {
		st.Pos = v
	}
	acc := uint32(in.U8(x)) + uint32(in.U16BE(x + 2))
	switch y {
	case 0, 1:
		acc += p.Base
	default:
		acc += table[x&3]
	}
	for i := int32(0); i < 4; i++ {
		if i == 2 {
			continue
		}
		acc += uint32(min(i, st.Pos)) + narrow(uint8(acc), 3, int16(i))
	}
	acc += uint32(min(max(p.Inner.Scale, -1), 1) + max(p.Inner.Scale, -p.Inner.Scale))
	if p.On {
		acc++
	}
	out[x] = acc
	in.PutU8(x, uint8(acc))
}
`

func emitSrc(t *testing.T) *Output {
	t.Helper()
	return emitSource(t, entrySrc)
}

// emitSource emits one subpackage source over the test parent, its
// entry point Entry.
func emitSource(t *testing.T, sub string) *Output {
	t.Helper()
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(sub)}}, Entries: []string{"Entry"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEmitVariableBuffers(t *testing.T) {
	parent, sub := conformance.VariableSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Entries[len(out.Entries)-1].BufferLengths {
		t.Fatal("missing length binding metadata")
	}
	for _, want := range []string{
		"constant uint* lengths [[buffer(4)]]", "WordView{b0, lengths[0]}", "WordView{b1, lengths[1]}",
		"static inline uint rt_view_load(WordView w, uint i) { return rt_load(w.data, i, w.length - 1u); }",
		"static inline void rt_view_store(WordView w, uint i, uint v) { rt_store(w.data, i, v, w.length - 1u); }",
		"static inline void rt_view_atomic_max(WordView w, uint i, uint v) { rt_atomic_max(w.data, i, v, w.length - 1u); }",
		"static inline uint rt_view_atomic_add(WordView w, uint i, uint v) { return rt_atomic_add(w.data, i, v, w.length - 1u); }",
		"static inline uint rt_view_atomic_xchg(WordView w, uint i, uint v) { return rt_atomic_xchg(w.data, i, v, w.length - 1u); }",
		"rt_view_load(", "rt_view_store(",
	} {
		if !strings.Contains(out.Source, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Only the prelude view helpers reach a view's words, each through
	// the fixed helper with the view's mask, so every access a compute
	// function makes wraps at the bound the binding supplied.
	if n := strings.Count(out.Source, "w.data"); n != 5 {
		t.Errorf("%d view data uses, want the 5 in the prelude helpers", n)
	}
}

// byteParentSrc and byteEntrySrc use a variable-length byte buffer
// beside a variable-length word buffer: its accessors, its length,
// and passed on to a helper.
const byteParentSrc = `package compute

type Bytes []byte
type Variable []uint32

func (b Bytes) U8(a uint32) uint8 { return b[int(a)&(len(b)-1)] }

func (b Bytes) U16BE(a uint32) uint16 {
	return uint16(b[int(a)&(len(b)-1)])<<8 | uint16(b[int(a+1)&(len(b)-1)])
}

func (b Bytes) PutU16BE(a uint32, v uint16) {
	b[int(a)&(len(b)-1)], b[int(a+1)&(len(b)-1)] = uint8(v>>8), uint8(v)
}
`

const byteEntrySrc = `package sub

import "example.com/compute"

func put(b compute.Bytes, a uint32, v uint16) { b.PutU16BE(a, v) }

func Entry(b compute.Bytes, w compute.Variable, x uint32) {
	n := uint32(len(b))
	w[x] = uint32(b.U8(x)) + uint32(b.U16BE(x+n)) + n
	put(b, x, uint16(x))
}
`

// emitBytes emits the byte buffer sources.
func emitBytes(t *testing.T) *Output {
	t.Helper()
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(byteParentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(byteEntrySrc)}}, Entries: []string{"Entry"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEmitVariableByteBuffers checks a variable-length byte buffer:
// a byte view of its bound bytes, the word count the binding supplies
// by four, and its accesses through the view helpers.
func TestEmitVariableByteBuffers(t *testing.T) {
	out := emitBytes(t)
	if !out.Entries[0].BufferLengths {
		t.Fatal("missing length binding metadata")
	}
	for _, want := range []string{
		"struct ByteView { device uchar* data; uint length; };",
		"static inline uint rt_view_u8(ByteView b, uint a) { return rt_u8(b.data, a, b.length - 1u); }",
		"static inline uint rt_view_u16be(ByteView b, uint a) { return rt_u16be(b.data, a, b.length - 1u); }",
		"static inline uint rt_view_u32be(ByteView b, uint a) { return rt_u32be(b.data, a, b.length - 1u); }",
		"static inline void rt_view_put_u8(ByteView b, uint a, uint v) { rt_put_u8(b.data, a, v, b.length - 1u); }",
		"static inline void rt_view_put_u16be(ByteView b, uint a, uint v) { rt_put_u16be(b.data, a, v, b.length - 1u); }",
		"static inline void rt_view_put_u32be(ByteView b, uint a, uint v) { rt_put_u32be(b.data, a, v, b.length - 1u); }",
		"device uchar* b0 [[buffer(0)]]", "ByteView{b0, lengths[0] * 4u}", "WordView{b1, lengths[1]}",
		"static void fn0(ByteView l0, uint l1, uint l2)",
		"rt_view_put_u16be(l0, ", "rt_view_u8(l0, ", "rt_view_u16be(l0, ", "l0.length",
	} {
		if !strings.Contains(out.Source, want) {
			t.Errorf("missing %q in\n%s", want, out.Source)
		}
	}
	if n := strings.Count(out.Source, "b.data"); n != 6 {
		t.Errorf("%d byte view data uses, want the 6 in the prelude helpers", n)
	}
}

func TestEmitEntry(t *testing.T) {
	out := emitSrc(t)
	src := out.Source
	for _, want := range []string{
		"#include <metal_stdlib>",
		"struct S0 { // Inner\n\tlong f0; // Scale\n};",
		"struct S1 { // Params\n\tuint f0; // Base\n\tarray<int, 2> f1; // Ratio\n\tS0 f2; // Inner\n\tbool f3; // On\n};",
		"constant array<uint, 4> tb0 = {1u, 2u, 4u, 8u}; // table",
		"static void fn0(thread S3& l0);",
		"static void fn1(int l0, thread int& r0, thread bool& r1);",
		"static uint fn2(uint l0, uint l1, int l2);",
		"static void fn3(device uchar* l0, device uint* l1, thread S1& l2, thread S2& l3, uint l4, int l5);",
		"l0.f0 = ((int)((uint)l0.f0 + (uint)l0.f1));",
		"int rv0 = ((int)((uint)l0 + (uint)1));\n\tbool rv1 = (l0 > 0);\n\tr0 = rv0;\n\tr1 = rv1;\n\treturn;",
		"uint l3 = ((l0 + l1) & 0xffu);",
		"fn1(l6.f0, l7, l8);",
		"if ((l8 && (l2.f1[(l7 & 1)] > 0)) || (l5 == 2)) {",
		"if (((int)(short)((int)((uint)((int)(short)l3) * (uint)l2))) == (-3)) {",
		"return (((uint)(l3 & ~l1)) | ((uint)l2));",
		"uint l9 = rt_u8(l0, l4, 1023u);",
		"rt_u16be(l0, (l4 + 2u), 1023u)",
		"switch (l5) {\n\tcase 0:\n\tcase 1:\n\t{",
		"tb0[(l4 & 3u)]",
		"for (; (l11 < 4); l11 = ((int)((uint)l11 + (uint)1))) {",
		"continue;",
		"min(l11, l3.f0)",
		"((long)((ulong)min(max(l2.f2.f0, (-1L)), 1L) + (ulong)max(l2.f2.f0, ((long)(-(ulong)l2.f2.f0)))))",
		"if (l2.f3) {",
		"rt_store(l1, l4, l10, 255u);",
		"rt_put_u8(l0, l4, ((uint)l10 & 0xffu), 1023u);",
		"kernel void k0( // Entry\n\tdevice uchar* b0 [[buffer(0)]],\n\tdevice uint* b1 [[buffer(1)]],\n\tdevice const uchar* pp [[buffer(2)]],\n\tconstant Dispatch& d [[buffer(3)]],\n\tuint2 gid [[thread_position_in_grid]])",
		"fn4(b0, b1, pp, d.bx, d.by, d.off, d.stride, d.seg, gid.x, gid.y, 0u);",
		"static void fn4(device uchar* l0, device uint* l1, device const uchar* l2, uint l3, uint l4, uint l5, uint l6, uint l7, uint l8, uint l9, uint l10) { // Entry.wrapper",
		"if (l9 >= l4) {\n\t\treturn;\n\t}",
		"uint l11 = (l8 * l7);\n\tif (l11 >= l3) {",
		"uint l12 = min((l11 + l7), l3);",
		"S1 l13 = (*(device const S1*)(l2 + (l5 + (l9 * l6))));",
		"S2 l14 = S2{};",
		"fn3(l0, l1, l13, l14, l11, ((int)l12));",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("source lacks:\n%s\n---\n%s", want, src)
		}
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %d", len(out.Entries))
	}
	k := out.Entries[0]
	if k.Name != "k0" || k.ParamsBinding != 2 || k.DispatchBinding != 3 || k.Workgroup != [2]int{8, 8} || len(k.Buffers) != 2 {
		t.Errorf("entry point record %+v", k)
	}
}

// TestEmitSignedWrap pins the printing of signed arithmetic through
// the unsigned type of its register width: add, subtract, multiply,
// negate, and shift left on int32, int64, and a narrow type, while
// unsigned arithmetic, the bitwise operators, and shift right print
// plain.
func TestEmitSignedWrap(t *testing.T) {
	src := emitSource(t, `package sub

import "example.com/compute"

type P struct {
	A int32
	B int32
	L int64
	U uint32
}

func Entry(w *compute.Words, p *P, x uint32) {
	a := p.A
	b := p.B
	h := int16(p.A)
	w[0] = uint32(a + b)
	w[1] = uint32(a - b)
	w[2] = uint32(a * b)
	w[3] = uint32(-a)
	w[4] = uint32(a << x)
	w[5] = uint32(p.L + p.L*2)
	w[6] = uint32(-p.L)
	w[7] = uint32(int32(h * h))
	w[8] = p.U + p.U*p.U - x
	w[9] = uint32(a&b) | uint32(a>>x)
}
`).Source
	for _, want := range []string{
		"((int)((uint)l3 + (uint)l4))",
		"((int)((uint)l3 - (uint)l4))",
		"((int)((uint)l3 * (uint)l4))",
		"((int)(-(uint)l3))",
		"((int)((uint)l3 << l2))",
		"((long)((ulong)l1.f2 + (ulong)((long)((ulong)l1.f2 * (ulong)2L))))",
		"((long)(-(ulong)l1.f2))",
		"((int)(short)((int)((uint)l5 * (uint)l5)))",
		"((l1.f3 + (l1.f3 * l1.f3)) - l2)",
		"(((uint)(l3 & l4)) | ((uint)(l3 >> l2)))",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("source lacks:\n%s\n---\n%s", want, src)
		}
	}
}

func TestEmitDeterministic(t *testing.T) {
	a := emitSrc(t).Source
	b := emitSrc(t).Source
	if a != b {
		t.Fatal("emitted source differs between runs")
	}
}

// TestEmitConformance emits the conformance module and checks one
// shell per entry with the bindings the entries declare and the
// group entry's shell.
func TestEmitConformance(t *testing.T) {
	mod, err := conformance.Lower()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != len(conformance.Entries) {
		t.Fatalf("entries = %d, want %d", len(out.Entries), len(conformance.Entries))
	}
	for i, k := range out.Entries {
		wg := [2]int{64, 1}
		switch {
		case mod.Entries[i].Span:
			wg = [2]int{8, 8}
		case mod.Entries[i].Group:
			wg = [2]int{128, 1}
		}
		if len(k.Buffers) != 3 || k.ParamsBinding != 3 || k.DispatchBinding != 4 || k.Workgroup != wg {
			t.Errorf("entry point %s: %+v", k.Name, k)
		}
	}
	if !strings.Contains(out.Source, "uint2 lid [[thread_position_in_threadgroup]]") || !strings.Contains(out.Source, "threadgroup_barrier(mem_flags::mem_device);") {
		t.Error("group entry shell or barrier missing")
	}
}

// TestEmitComputePackages emits the real compute packages and checks
// the shells come out with the bindings the entries declare.
func TestEmitComputePackages(t *testing.T) {
	const root = "github.com/user-none/erings/core/compute"
	prog, err := compile.Load(
		compile.Source{Path: root, Files: compute.Source},
		compile.Source{Path: root + "/screen", Files: screen.Source, Entries: []string{
			"BuildPalette", "NBGCellSpan", "NBGBitmapSpan", "RBGCellSpan", "RBGBitmapSpan",
			"CompositeSpan", "GradationSpan", "CopyVRAMPages", "CopyCRAMPages",
		}},
		compile.Source{Path: root + "/draw", Files: draw.Source, Entries: draw.Entries[:]},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	if want := 9 + len(draw.Entries); len(out.Entries) != want {
		t.Fatalf("entries = %d, want %d", len(out.Entries), want)
	}
	if !strings.Contains(out.Source, "kernel void k5( // CompositeSpan") {
		t.Error("composite shell missing")
	}
	if !strings.Contains(out.Source, "kernel void k9( // Generate") || !strings.Contains(out.Source, "rt_atomic_max(") {
		t.Error("generate shell or the atomic max helper call missing")
	}
	if strings.Contains(out.Source, "?") {
		t.Error("source contains an unprinted node")
	}
}
