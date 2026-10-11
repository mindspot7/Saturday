// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hlsl

import (
	"regexp"
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

func pair(a int32) [2]int32 { return [2]int32{a, -a} }

func sum(p [2]int32) int32 { return p[0] + p[1] }

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
	s = stepper{}
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
	acc += uint32(sum([2]int32{y, 1}))
	q := pair(y)
	acc += uint32(q[1])
	acc += uint32(sum(pair(y)))
	acc += uint32(pair(y)[0])
	q = pair(y)
	acc += uint32(uint64(y) >> 40)
	acc += uint32(int64(y) - -0x123456789ABCDEF + 0x7FFFFFFF)
	out[x] = acc
	in.PutU8(x, uint8(acc))
}
`

func emitSrc(t *testing.T) *Output {
	t.Helper()
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(entrySrc)}}, Entries: []string{"Entry"}},
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

// TestEmitVariableByteBuffers checks a variable-length byte buffer:
// its hidden count is the word count the binding supplies by four,
// and its accesses go through the view helpers with that count.
func TestEmitVariableByteBuffers(t *testing.T) {
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
	k := out.Entries[0]
	if k.Layout != (Layout{Buffers: 2, Constants: FixedWords + 2, Lengths: true, Dynamic: 0b11, Workgroup: [2]int{64, 1}}) {
		t.Fatalf("byte entry %+v", k.Layout)
	}
	for _, want := range []string{
		"uint rt_view_u8(RWByteAddressBuffer b, uint n, uint a) { return rt_u8(b, a, n - 1u); }",
		"uint rt_view_u16be(RWByteAddressBuffer b, uint n, uint a) { return rt_u16be(b, a, n - 1u); }",
		"uint rt_view_u32be(RWByteAddressBuffer b, uint n, uint a) { return rt_u32be(b, a, n - 1u); }",
		"void rt_view_put_u8(RWByteAddressBuffer b, uint n, uint a, uint v) { rt_put_u8(b, a, v, n - 1u); }",
		"void rt_view_put_u16be(RWByteAddressBuffer b, uint n, uint a, uint v) { rt_put_u16be(b, a, v, n - 1u); }",
		"void rt_view_put_u32be(RWByteAddressBuffer b, uint n, uint a, uint v) { rt_put_u32be(b, a, v, n - 1u); }",
		"RWByteAddressBuffer b0 : register(u0);",
		"(b0, d_len0 * 4u, b1, d_len1, d_bx",
		"RWByteAddressBuffer l0, uint l0_n, uint l1, uint l2",
		"rt_view_put_u16be(l0, l0_n, ", "rt_view_u8(l0, l0_n, ", "rt_view_u16be(l0, l0_n, ",
	} {
		if !strings.Contains(k.Source, want) {
			t.Errorf("missing %q in\n%s", want, k.Source)
		}
	}
}

func TestEmitEntry(t *testing.T) {
	out := emitSrc(t)
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %d", len(out.Entries))
	}
	src := out.Entries[0].Source
	for _, want := range []string{
		"uint rt_u8(RWByteAddressBuffer b, uint a, uint m) {",
		"struct S0 { // Inner\n\tint64_t f0; // Scale\n};",
		"struct S1 { // Params\n\tuint f0; // Base\n\tint f1[2]; // Ratio\n\tS0 f2; // Inner\n\tbool f3; // On\n};",
		"S0 ld0(RWByteAddressBuffer b, uint a) { // Inner\n\tS0 r;\n\tr.f0 = (int64_t)rt_load64(b, a + 0u);\n\treturn r;\n}",
		"S1 ld1(RWByteAddressBuffer b, uint a) { // Params\n\tS1 r;\n\tr.f0 = b.Load(a + 0u);\n\tr.f1[0] = asint(b.Load(a + 4u));\n\tr.f1[1] = asint(b.Load(a + 8u));\n\tr.f2 = ld0(b, a + 16u);\n\tr.f3 = rt_u8(b, a + 24u, 0xffffffffu) != 0u;\n\treturn r;\n}",
		"static const uint tb0[4] = {1u, 2u, 4u, 8u}; // table",
		"void fn0(inout S3 l0);",
		"void fn1(int l0, out int r0, out bool r1);",
		"void fn2(int l0, out int r0[2]);",
		"int fn3(int l0[2]);",
		"uint fn4(uint l0, uint l1, int l2);",
		"void fn5(RWByteAddressBuffer l0, RWStructuredBuffer<uint> l1, inout S1 l2, inout S2 l3, uint l4, int l5);",
		"l0.f0 = ((int)((uint)l0.f0 + (uint)l0.f1));",
		"int rv0 = ((int)((uint)l0 + (uint)1L));\n\tbool rv1 = (l0 > 0);\n\tr0 = rv0;\n\tr1 = rv1;\n\treturn;",
		"int rv0[2] = {l0, ((int)(-(uint)l0))};\n\tr0 = rv0;\n\treturn;",
		"uint l3 = ((l0 + l1) & 0xffu);",
		"S3 l6 = {((int)(l4)), 1L};",
		"fn0(l6);",
		"S3 h0 = {0, 0};\n\tl6 = h0;",
		"fn1(l6.f0, l7, l8);",
		"if ((l8 && (asint(pp.Load((rt_prow + 4u + ((uint)(l7 & 1L) * 4u)))) > 0)) || (l5 == 2L)) {",
		"if (((int)((uint)((int)((uint)((int)((uint)(l3) << 16) >> 16) * (uint)l2)) << 16) >> 16) == (-3L)) {",
		"return (((uint)(l3 & ~l1)) | ((uint)(l2)));",
		"uint l9 = rt_u8(l0, l4, 1023u);",
		"rt_u16be(l0, (l4 + 2u), 1023u)",
		"switch (l5) {\n\tcase 0:\n\tcase 1L:\n\t{",
		"tb0[(l4 & 3u)]",
		"for (; (l11 < 4L); l11 = ((int)((uint)l11 + (uint)1L))) {",
		"continue;",
		"min(l11, l3.f0)",
		"((int64_t)((uint64_t)rt_min_i64(rt_max_i64(l2.f2.f0, ((int64_t)(-1L))), ((int64_t)1L)) + (uint64_t)rt_max_i64(l2.f2.f0, ((int64_t)(-(uint64_t)l2.f2.f0)))))",
		"if (l2.f3) {",
		"int h1[2] = {l5, 1L};\n\tl10 = (l10 + ((uint)(fn3(h1))));",
		"int l12[2];\n\tfn2(l5, l12);",
		// The lowering's own evaluation-order temporary carries the
		// inner call of sum(pair(y)); the emitter's hoist carries the
		// indexed call.
		"int l13[2];\n\tfn2(l5, l13);\n\tl10 = (l10 + ((uint)(fn3(l13))));",
		"int h2[2];\n\tfn2(l5, h2);\n\tl10 = (l10 + ((uint)(h2[0])));",
		"((uint64_t)((int64_t)(l5)))",
		"((int64_t)(((uint64_t)(4275878552u) << 32) | (uint64_t)(1985229329u)))",
		"((int64_t)2147483647L)",
		"rt_store(l1, l4, l10, 255u);",
		"rt_put_u8(l0, l4, (((uint)(l10)) & 0xffu), 1023u);",
		"RWByteAddressBuffer b0 : register(u0);\nRWStructuredBuffer<uint> b1 : register(u1);\ncbuffer Dispatch",
		"cbuffer Dispatch : register(b0) {\n\tuint d_bx; uint d_by; uint d_off; uint d_stride; uint d_seg; uint d_rowthreads; uint d_base;\n};",
		"[numthreads(8, 8, 1)]\n[RootSignature(\"RootFlags(0), UAV(u0), UAV(u1), UAV(u2), RootConstants(num32BitConstants=7, b0)\")]\nvoid main(uint3 dtid : SV_DispatchThreadID) { // Entry\n\tfn6(b0, b1, pp, d_bx, d_by, d_off, d_stride, d_seg, (dtid.x + d_base), dtid.y, 0u);\n}",
		"void fn6(RWByteAddressBuffer l0, RWStructuredBuffer<uint> l1, RWByteAddressBuffer l2, uint l3, uint l4, uint l5, uint l6, uint l7, uint l8, uint l9, uint l10) { // Entry.wrapper",
		"if (l9 >= l4) {\n\t\treturn;\n\t}",
		"uint l11 = (l8 * l7);\n\tif (l11 >= l3) {",
		"uint l12 = min((l11 + l7), l3);",
		"S1 l13 = ld1(l2, (l5 + (l9 * l6)));\n\trt_prow = (l5 + (l9 * l6));",
		"static uint rt_prow;",
		"S2 l14 = {0};",
		"fn5(l0, l1, l13, l14, l11, ((int)(l12)));",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("source lacks:\n%s\n---\n%s", want, src)
		}
	}
	if !strings.HasPrefix(src, "RWByteAddressBuffer pp : register(u2);\nuint rt_u8(") {
		t.Error("the params buffer is not declared ahead of the common source")
	}
	// The declaration and the assignment of q are both the statement
	// form of the out-result call.
	if n := strings.Count(src, "fn2(l5, l12);"); n != 2 {
		t.Errorf("%d statement-form calls into l12, want 2", n)
	}
	k := out.Entries[0]
	if k.Entry != 0 || k.Func != "Entry" {
		t.Errorf("entry point record %+v", k)
	}
	if k.Layout != (Layout{Buffers: 3, Constants: FixedWords, Workgroup: [2]int{8, 8}}) {
		t.Errorf("layout %+v", k.Layout)
	}
	if strings.Contains(src, "?") {
		t.Error("source contains an unprinted node")
	}
}

// indexEntrySrc reads and writes small arrays at run-time indexes in
// every position: a scalar read, a read through two indexes, struct
// and array values read into locals and passed by value, a whole
// element and a field written, an element passed by reference to a
// function that writes through it and to one that only reads, and an
// array longer than the chain form takes.
const indexEntrySrc = `package sub

import "example.com/compute"

type Cand struct {
	Pri uint32
	On  bool
}

type Params struct {
	Vals  [5]uint32
	Flags [6]bool
	Rows  [3][4]uint32
	Big   [16]uint32
	Cands [3]Cand
}

func bump(c *Cand) { c.Pri++ }

func above(a, b *Cand) bool { return a.Pri > b.Pri }

func pick(c Cand) uint32 {
	if c.On {
		return c.Pri
	}
	return 0
}

func Entry(out *compute.Words, p *Params, x uint32) {
	var cs [4]Cand
	i := x & 3
	cs[i] = Cand{Pri: p.Vals[x%5], On: p.Flags[x%6]}
	cs[i].Pri += 2
	c := p.Cands[x%3]
	row := p.Rows[x%3]
	acc := pick(cs[(i+1)&3]) + c.Pri + row[x&3] + p.Rows[x%3][x&3]
	bump(&cs[i])
	if above(&cs[i], &cs[(i+1)&3]) {
		acc++
	}
	acc += p.Big[x&15]
	out[x&255] = acc + cs[0].Pri
}
`

// TestEmitLoopPostIndexedTarget emits a loop whose post stores a
// struct element at a run-time index: the target is a place printed
// in the indexed form, which a post statement prints as it stands.
func TestEmitLoopPostIndexedTarget(t *testing.T) {
	const src = `package sub
import "example.com/compute"
type Cand struct{ Pri uint32 }
func Entry(out *compute.Words, x uint32) {
	var cs [4]Cand
	var c Cand
	for i := uint32(0); i < 4; cs[i&3] = c {
		c.Pri += x
		i++
	}
	out[x] = cs[x&3].Pri
}
`
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(src)}}, Entries: []string{"Entry"}},
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
	if s := out.Entries[0].Source; !strings.Contains(s, "] = l") {
		t.Fatalf("the post's indexed store is missing:\n%s", s)
	}
}

// TestEmitRuntimeIndexChains checks that an array of at most selectMax
// elements in a path rooted in a local is never indexed at a run-time
// index: reads print as select chains or if chains into a local, a
// reference argument the callee only reads as a copy, and writes as if
// chains over the constant indexes. A reference argument the callee
// writes through and a longer array keep the indexed form.
func TestEmitRuntimeIndexChains(t *testing.T) {
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(indexEntrySrc)}}, Entries: []string{"Entry"}},
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
	src := out.Entries[0].Source
	if strings.Contains(src, "?") {
		t.Fatalf("source contains an unprinted node:\n%s", src)
	}
	start := strings.Index(src, "{ // Entry\n")
	if start < 0 {
		t.Fatalf("no Entry function:\n%s", src)
	}
	body := src[start : start+strings.Index(src[start:], "\n}\n")]
	for _, want := range []string{"select(", "} else if (", "} else {"} {
		if !strings.Contains(body, want) {
			t.Errorf("Entry lacks %q:\n%s", want, body)
		}
	}
	constIndex := regexp.MustCompile(`^\(?-?[0-9]+[uL]?\)?$`)
	index := regexp.MustCompile(`(\.f[0-9]+|l[0-9]+|h[0-9]+)\[([^\[\]]+)\]`)
	refCall := 0
	for _, line := range strings.Split(body, "\n") {
		for _, m := range index.FindAllStringSubmatch(line, -1) {
			if constIndex.MatchString(m[2]) {
				continue
			}
			switch {
			case m[1] == ".f3":
				// Big, sixteen elements, keeps the indexed form.
			case strings.Contains(line, "fn0("):
				// bump's reference argument keeps the indexed form.
				refCall++
			default:
				t.Errorf("run-time index outside the chain form: %s", strings.TrimSpace(line))
			}
		}
	}
	if refCall != 1 {
		t.Errorf("%d indexed reference arguments, want 1:\n%s", refCall, body)
	}
}

// paramsEntrySrc reads its params row at run-time indexes directly and
// through a helper passed the row, and passes a changed copy of the row
// to a helper that reads it the same way.
const paramsEntrySrc = `package sub

import "example.com/compute"

type Pair struct {
	A, B uint32
}

type Row struct {
	Vals  [4]uint32
	Pairs [2]Pair
	On    [3]bool
	Wide  [2]int64
}

func helper(r *Row, i uint32) uint32 { return r.Vals[i&3] }

func copied(r *Row, i uint32) uint32 { return r.Vals[i&3] }

func Entry(out *compute.Words, p *Row, x uint32) {
	acc := p.Vals[x&3] + helper(p, x)
	q := p.Pairs[x&1]
	acc += q.A
	if p.On[x%3] {
		acc++
	}
	acc += uint32(p.Wide[x&1])
	c := *p
	c.Vals[0] = 7
	acc += copied(&c, x)
	out[x&255] = acc
}
`

// TestEmitParamsInPlace checks that a read of the params row at a
// run-time index is a load from the params buffer at the row's address,
// in the function the row is passed to as well, in the form of the
// field's type, while a copy of the row is read as the copy.
func TestEmitParamsInPlace(t *testing.T) {
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(paramsEntrySrc)}}, Entries: []string{"Entry"}},
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
	src := out.Entries[0].Source
	if strings.Contains(src, "?") {
		t.Fatalf("source contains an unprinted node:\n%s", src)
	}
	if !strings.HasPrefix(src, "RWByteAddressBuffer pp : register(u1);\n") {
		t.Errorf("the params buffer is not declared ahead of the common source:\n%s", src)
	}
	body := func(name string) string {
		start := strings.Index(src, "{ // "+name+"\n")
		if start < 0 {
			t.Fatalf("no function %s:\n%s", name, src)
		}
		return src[start : start+strings.Index(src[start:], "\n}\n")]
	}
	entry, helper, copied, wrapper := body("Entry"), body("helper"), body("copied"), body("Entry.wrapper")
	for _, want := range []string{"pp.Load((rt_prow + 0u + ", "rt_u8(pp, (rt_prow + ", "rt_load64(pp, (rt_prow + ", "(pp, (rt_prow + 16u + "} {
		if !strings.Contains(entry, want) {
			t.Errorf("Entry lacks %q:\n%s", want, entry)
		}
	}
	if !strings.Contains(helper, "pp.Load((rt_prow + 0u + ") {
		t.Errorf("helper, passed the row, does not read it in place:\n%s", helper)
	}
	if strings.Contains(copied, "pp") {
		t.Errorf("copied, passed a changed copy, reads the params buffer:\n%s", copied)
	}
	if !strings.Contains(wrapper, "rt_prow = ") {
		t.Errorf("the wrapper does not set the row's address:\n%s", wrapper)
	}
}

// TestEmitLoopTemporaries checks that a loop whose condition or post
// needs a temporary prints in the general form, the post at the top of
// every pass but the first and the condition a break, and that a loop
// that needs none keeps the for header.
func TestEmitLoopTemporaries(t *testing.T) {
	const src = `package sub
import "example.com/compute"
type Cand struct{ Pri uint32 }
func sum(p [2]uint32) uint32 { return p[0] + p[1] }
func pick(c Cand) uint32 { return c.Pri }
func Entry(out *compute.Words, x uint32) {
	var cs [4]Cand
	n := uint32(0)
	for i := uint32(0); sum([2]uint32{i, x}) < 8; i++ {
		n++
	}
	for i := uint32(0); pick(cs[i&3]) < x; i++ {
		cs[i&3].Pri++
	}
	p := [2]uint32{}
	for i := uint32(0); i < 4; p = [2]uint32{i, p[0]} {
		i++
		if i == 2 {
			continue
		}
		n += p[1]
	}
	for i := uint32(0); i < 4; i++ {
		n++
	}
	out[x&255] = n + cs[0].Pri
}
`
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(src)}}, Entries: []string{"Entry"}},
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
	s := out.Entries[0].Source
	general := regexp.MustCompile(`for \(bool (h\d+) = false; ; (h\d+) = true\) \{`).FindAllStringSubmatch(s, -1)
	if len(general) != 3 {
		t.Fatalf("%d loops in the general form, want 3:\n%s", len(general), s)
	}
	for _, g := range general {
		if g[1] != g[2] {
			t.Errorf("the flag %s is declared but %s is set", g[1], g[2])
		}
	}
	if !regexp.MustCompile(`if \(` + general[2][1] + `\) \{\n(\t+.*\n)*?\t+l\d+ = h\d+;\n\t+\}\n\t+if \(!\(l\d+ < 4u\)\) \{\n\t+break;`).MatchString(s) {
		t.Errorf("the post is not at the top of the pass ahead of the condition's break:\n%s", s)
	}
	if !strings.Contains(s, "continue;") {
		t.Errorf("the post loop's continue is missing:\n%s", s)
	}
	if !regexp.MustCompile(`for \(; \(l\d+ < 4u\); l\d+ = \(l\d+ \+ 1u\)\) \{`).MatchString(s) {
		t.Errorf("the loop that needs no temporary lost its for header:\n%s", s)
	}
}

// TestEmitParamsArrayAssign checks that an array of the params row
// read at a run-time index the destination holds is loaded into a
// temporary and assigned once, so no element's address reads an
// element already stored.
func TestEmitParamsArrayAssign(t *testing.T) {
	const src = `package sub
import "example.com/compute"
type Row struct{ Rows [3][4]uint32 }
func Entry(out *compute.Words, p *Row, x uint32) {
	q := [4]uint32{x & 1}
	q = p.Rows[q[0]]
	out[x&255] = q[1]
}
`
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(src)}}, Entries: []string{"Entry"}},
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
	s := out.Entries[0].Source
	if regexp.MustCompile(`l\d+\[\d\] = pp\.Load`).MatchString(s) {
		t.Errorf("the row's elements are stored into the destination one at a time:\n%s", s)
	}
	m := regexp.MustCompile(`(h\d+)\[3\] = pp\.Load\(.*\n\s*l\d+ = (h\d+);`).FindStringSubmatch(s)
	if m == nil || m[1] != m[2] {
		t.Errorf("the destination is not assigned the loaded temporary once:\n%s", s)
	}
}

func TestEmitDeterministic(t *testing.T) {
	a := emitSrc(t).Entries[0].Source
	b := emitSrc(t).Entries[0].Source
	if a != b {
		t.Fatal("emitted source differs between runs")
	}
}

// TestEmitVariableBuffers checks the hidden word count beside every
// variable-length buffer: the shell's length words, the wrapper's
// and the entry's parameters, and the view helpers.
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
	k := out.Entries[len(out.Entries)-1]
	if k.Func != conformance.VariableEntry || !k.Layout.Lengths || k.Layout.Constants != FixedWords+3 || k.Layout.Buffers != 3 {
		t.Fatalf("variable entry %+v", k.Layout)
	}
	for _, want := range []string{
		"\tuint d_len0;\n\tuint d_len1;\n\tuint d_len2;\n};",
		"RootConstants(num32BitConstants=10, b0)",
		"(b0, d_len0, b1, d_len1, pp, d_bx",
		"RWStructuredBuffer<uint> l0, uint l0_n, RWStructuredBuffer<uint> l1, uint l1_n, inout S",
		"rt_view_load(l0, l0_n, ",
		"rt_view_store(l1, l1_n, ",
		"(l3 & (l4 - 1u))) ^ l4) ^ l1_n)",
	} {
		if !strings.Contains(k.Source, want) {
			t.Errorf("missing %q in\n%s", want, k.Source)
		}
	}
	// The view helpers reach a view's words through the fixed helpers
	// with the view's mask, so no view is indexed directly.
	for _, want := range []string{
		"uint rt_view_load(RWStructuredBuffer<uint> w, uint n, uint i) { return rt_load(w, i, n - 1u); }",
		"void rt_view_store(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { rt_store(w, i, v, n - 1u); }",
		"void rt_view_atomic_max(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { rt_atomic_max(w, i, v, n - 1u); }",
		"uint rt_view_atomic_add(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { return rt_atomic_add(w, i, v, n - 1u); }",
		"uint rt_view_atomic_xchg(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { return rt_atomic_xchg(w, i, v, n - 1u); }",
	} {
		if !strings.Contains(k.Source, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(k.Source, "w[i]"); n != 0 {
		t.Errorf("%d direct view indexings, want none", n)
	}
	// Every entry of the module declares the module's constant count.
	for _, k := range out.Entries {
		if k.Layout.Constants != FixedWords+3 {
			t.Errorf("%s declares %d constants, want %d", k.Func, k.Layout.Constants, FixedWords+3)
		}
	}
}

// TestEmitConformance emits the conformance module and checks one
// source per entry with the layout the entries declare, and the group
// entry's shell and barrier.
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
		want := Layout{Buffers: 4, Constants: FixedWords + 3, Lengths: true, Dynamic: 0b101, Group: mod.Entries[i].Group, Workgroup: wg}
		if k.Layout != want || k.Func != conformance.Entries[i].Name {
			t.Errorf("entry point %s: %+v", k.Func, k)
		}
		if strings.Contains(k.Source, "?") {
			t.Errorf("%s: source contains an unprinted node", k.Func)
		}
	}
	var group, wide EntryPoint
	for _, k := range out.Entries {
		switch k.Func {
		case "Group":
			group = k
		case "Wide":
			wide = k
		}
	}
	if !strings.Contains(group.Source, "dtid.x, dtid.y, d_rowthreads);") || !strings.Contains(group.Source, "DeviceMemoryBarrierWithGroupSync();") || !strings.Contains(group.Source, "[numthreads(128, 1, 1)]") {
		t.Error("group entry shell or barrier missing")
	}
	if !strings.Contains(wide.Source, "(dtid.x + d_base), dtid.y, 0u);") {
		t.Error("index entry shell does not add the thread base")
	}
}

// TestEmitComputePackages emits the real compute packages and checks
// the shells come out with the layouts the entries declare.
func TestEmitComputePackages(t *testing.T) {
	const root = "github.com/user-none/erings/core/compute"
	prog, err := compile.Load(
		compile.Source{Path: root, Files: compute.Source},
		compile.Source{Path: root + "/screen", Files: screen.Source, Entries: screen.Entries[:]},
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
	if want := len(screen.Entries) + len(draw.Entries); len(out.Entries) != want {
		t.Fatalf("entries = %d, want %d", len(out.Entries), want)
	}
	byName := map[string]EntryPoint{}
	for _, k := range out.Entries {
		byName[k.Func] = k
		if strings.Contains(k.Source, "?") {
			t.Errorf("%s: source contains an unprinted node", k.Func)
		}
		if k.Layout.Constants != FixedWords+Lengths(mod) {
			t.Errorf("%s declares %d constants", k.Func, k.Layout.Constants)
		}
	}
	composite := byName["CompositeSpan"]
	if composite.Layout != (Layout{Buffers: 13, Constants: FixedWords + Lengths(mod), Lengths: true, Dynamic: 0b100111111111, Workgroup: [2]int{8, 8}}) || !strings.Contains(composite.Source, "void main(uint3 dtid : SV_DispatchThreadID) { // CompositeSpan") {
		t.Errorf("composite shell %+v", composite.Layout)
	}
	tag := byName["GenerateTag"]
	if !tag.Layout.Group || !tag.Layout.Lengths || !strings.Contains(tag.Source, "rt_atomic_max(") {
		t.Errorf("generate shell %+v or the atomic max helper call missing", tag.Layout)
	}
}

// TestRootSignatureLimit checks that a layout past 64 dwords is
// refused at emission rather than by the compiler.
func TestRootSignatureLimit(t *testing.T) {
	if err := (Layout{Buffers: 28, Constants: 9}).check(); err == nil {
		t.Error("65 dwords accepted")
	}
	if err := (Layout{Buffers: 28, Constants: 8}).check(); err != nil {
		t.Error(err)
	}
}
