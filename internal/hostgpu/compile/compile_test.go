// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"strings"
	"testing"
	"testing/fstest"
)

const testParentPath = "example.com/compute"

// testParent is a small parent package: buffer types with accessors
// and a shared-word helper in the parent's own style, and a buffer
// group.
const testParent = `package compute

const BufSize = 1024
const WordCount = 256

type Buf [BufSize]byte
type Words [WordCount]uint32
type Variable []uint32
type Bytes []byte

type Pair struct {
	In  Variable
	Out Variable
}

func (b *Buf) U8(a uint32) uint8 { return b[a&(BufSize-1)] }

func (b *Buf) PutU8(a uint32, v uint8) { b[a&(BufSize-1)] = v }

func (b Bytes) U16BE(a uint32) uint16 {
	m := len(b) - 1
	if m < 0 {
		return 0
	}
	return uint16(b[int(a)&m])<<8 | uint16(b[int(a+1)&m])
}

func (b Bytes) PutU16BE(a uint32, v uint16) {
	m := len(b) - 1
	if m < 0 {
		return
	}
	b[int(a)&m], b[int(a+1)&m] = uint8(v>>8), uint8(v)
}

func (w Variable) Add(i uint32, v uint32) uint32 {
	old := w[int(i)&(len(w)-1)]
	w[int(i)&(len(w)-1)] = old + v
	return old
}

func NextPhase() {}
`

// load runs the frontend on one subpackage source over the test parent.
func load(t *testing.T, sub string, entries ...string) error {
	t.Helper()
	parent := fstest.MapFS{"buffers.go": {Data: []byte(testParent)}}
	subFS := fstest.MapFS{"sub.go": {Data: []byte(sub)}}
	_, err := Load(
		Source{Path: testParentPath, Files: parent},
		Source{Path: testParentPath + "/sub", Files: subFS, Entries: entries},
	)
	return err
}

const validSub = `package sub

import "example.com/compute"

const shift = 3

var table = [4]uint32{1, 2, 4, 8}

type Params struct {
	Base  uint32
	Ratio [2]int32
	Inner Inner
	On    bool
}

type Inner struct {
	Scale int64
}

type State struct {
	Pos int32
}

type stepper struct {
	x, dx int32
}

func (s *stepper) step() { s.x += s.dx }
func (s stepper) value() int32 { return s.x }

func helper(p *Params, st *State, i int32) int32 {
	var st2 stepper = stepper{x: i, dx: 1}
	st2.step()
	return min(st2.value(), p.Ratio[i&1]) + abs32(int32(table[i&3]))
}

func abs32(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}

func addr(v *int32) { *v = *v + 1 }

func Entry(in *compute.Buf, out *compute.Words, p *Params, st *State, x uint32, y uint32) {
	var acc uint32
	b := in.U8(x)
	switch b {
	case 0:
		acc = p.Base
	case 1, 2:
		acc = uint32(int8(b)) << shift
	default:
		acc = uint32(b) >> 1
	}
	var n int32 = int32(x)
	addr(&n)
	addr(&st.Pos)
	for i := int32(0); i < 4; i++ {
		if i == 2 {
			continue
		}
		acc += uint32(helper(p, st, i))
		if acc > 100 {
			break
		}
	}
	for {
		st.Pos++
		if st.Pos > 3 {
			break
		}
	}
	for k := range int32(3) {
		acc += uint32(k)
	}
	if p.On {
		acc++
	}
	out[x+y] = acc
	out[x] += acc
}
`

func TestValidSubset(t *testing.T) {
	if err := load(t, validSub, "Entry"); err != nil {
		t.Fatal(err)
	}
}

// TestAliasingAllowed holds the overlapping references the aliasing
// rule permits: two references a callee only reads, which may be
// the same element, and references a callee writes through that are
// distinct constant indices or distinct fields.
func TestAliasingAllowed(t *testing.T) {
	const src = `package sub

type stepper struct {
	x, dx int32
}

type pair struct {
	a, b stepper
}

func ranks(a, b *stepper) bool { return a.x > b.x }
func bump(a, b *stepper)      { a.x += b.dx }

func Entry(x uint32) {
	var ss [2]stepper
	var p pair
	if ranks(&ss[x&1], &ss[0]) {
		bump(&ss[0], &ss[1])
	}
	bump(&p.a, &p.b)
	ranks(&p.a, &p.a)
}
`
	if err := load(t, src, "Entry"); err != nil {
		t.Fatal(err)
	}
}

// TestNextPhaseInGroupEntry holds NextPhase in the body of a group
// entry, the one place it is allowed.
func TestNextPhaseInGroupEntry(t *testing.T) {
	const src = "package sub\nimport \"example.com/compute\"\nfunc Entry(x, y, z uint32) {\n\tcompute.NextPhase()\n}\n"
	if err := load(t, src, "Entry"); err != nil {
		t.Fatal(err)
	}
}

// TestTableReads holds the uses of a table: an element, through
// parentheses too, and its length.
func TestTableReads(t *testing.T) {
	const src = `package sub

var tbl = [4]uint32{1, 2, 3, 4}

func Entry(x uint32) {
	y := tbl[x&3] + (tbl)[(x+1)&3] + uint32(len(tbl))
	_ = y
}
`
	if err := load(t, src, "Entry"); err != nil {
		t.Fatal(err)
	}
}

// TestPointerReceivers holds the receivers a pointer-receiver call
// takes the address of: a local, an element of a local array at a
// run-time index, and a field through a pointer parameter, besides a
// pointer receiver passed as is.
func TestPointerReceivers(t *testing.T) {
	const src = `package sub

type stepper struct {
	x, dx int32
}

type pair struct {
	a stepper
}

func (s *stepper) step() { s.x += s.dx }

func run(p *pair, q *stepper) {
	p.a.step()
	q.step()
}

func Entry(x uint32) {
	var s stepper
	var ss [2]stepper
	var pr pair
	s.step()
	ss[x&1].step()
	run(&pr, &s)
}
`
	if err := load(t, src, "Entry"); err != nil {
		t.Fatal(err)
	}
}

// TestCallOrderAccepted holds the statements the call-order rule
// allows: a call that writes storage the rest of the statement does
// not read, a plain target inside the storage a call writes, a call
// with no read outside it, and a word read of a buffer no call takes.
func TestCallOrderAccepted(t *testing.T) {
	const src = `package sub
import "example.com/compute"
type C struct{ N, M int32 }
type L struct {
	G      C
	Last   int32
	Cycles int32
}
func inc(c *C) int32 {
	c.N++
	return 1
}
func bump(p *uint32) uint32 {
	*p++
	return 1
}
func put(w compute.Variable, i uint32) uint32 {
	w[i] = 5
	return 0
}
func F(w, v compute.Variable, g *compute.Pair, i uint32) uint32 {
	var l L
	l.Cycles += inc(&l.G)
	l.G.N = max(inc(&l.G), l.Last)
	i = bump(&i)
	bump(&i)
	x := v[i&1] + put(w, i)
	x += g.In[i] + put(g.Out, i)
	return x + uint32(l.Cycles)
}
`
	if err := load(t, src); err != nil {
		t.Fatal(err)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"int local", "package sub\nfunc F() int32 {\n\tfor i := 0; i < 2; i++ {\n\t}\n\treturn 0\n}\n", "type int is outside"},
		{"float64", "package sub\nfunc F(x float64) {}\n", "type float64 is outside"},
		{"range over an array", "package sub\nfunc F(a [2]int32) {\n\tfor range a {\n\t}\n}\n", "range is allowed only over an integer"},
		{"range key of type int", "package sub\nfunc F() {\n\tfor i := range 4 {\n\t\t_ = i\n\t}\n}\n", "type int is outside"},
		{"range key a field", "package sub\ntype S struct{ X int32 }\nfunc F(s *S) {\n\tfor s.X = range int32(4) {\n\t}\n}\n", "a range key is a local"},
		{"closure", "package sub\nfunc F() {\n\tf := func() {}\n\tf()\n}\n", "closures"},
		{"recursion", "package sub\nfunc a(n int32) int32 {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn b(n)\n}\nfunc b(n int32) int32 { return a(n - 1) }\n", "recursive call chain: a -> b -> a"},
		{"byte buffer index", "package sub\nimport \"example.com/compute\"\nfunc F(b *compute.Buf) uint8 { return b[3] }\n", "byte buffer Buf is read and written through its accessors"},
		{"byte buffer write", "package sub\nimport \"example.com/compute\"\nfunc F(b *compute.Buf) { b[3] = 1 }\n", "byte buffer Buf is read and written through its accessors"},
		{"word buffer index of type int64", "package sub\nimport \"example.com/compute\"\nfunc F(w *compute.Words, i int64) uint32 { return w[i] }\n", "a buffer index is an int32 or uint32"},
		{"buffer local", "package sub\nimport \"example.com/compute\"\nfunc F(b *compute.Buf) {\n\tvar c compute.Buf = *b\n\t_ = c\n}\n", "buffers appear only as parameters"},
		{"fixed-size buffer by value", "package sub\nimport \"example.com/compute\"\nfunc F(b compute.Buf) {}\n", "fixed-size buffers and buffer groups are passed by pointer"},
		{"variable-length buffer by pointer", "package sub\nimport \"example.com/compute\"\nfunc F(v *compute.Variable) {}\n", "a variable-length buffer is passed by value"},
		{"buffer length unconverted", "package sub\nimport \"example.com/compute\"\nfunc F(v compute.Variable) bool { return len(v) > 0 }\n", "the length of a buffer is used as the operand of a conversion"},
		{"buffer length converted wide", "package sub\nimport \"example.com/compute\"\nfunc F(v compute.Variable) int64 { return int64(len(v)) }\n", "the length of a buffer converts to int32 or uint32"},
		{"group with a scalar", "package sub\nimport \"example.com/compute\"\ntype G struct {\n\tW *compute.Words\n\tN uint32\n}\n", "type G holds a buffer, so it is a buffer group"},
		{"group by value", "package sub\nimport \"example.com/compute\"\ntype G struct{ W *compute.Words }\nfunc F(g G) {}\n", "fixed-size buffers and buffer groups are passed by pointer"},
		{"pointer local", "package sub\ntype S struct{ X int32 }\nfunc F(p *S) {\n\tq := p\n\tq.X = 1\n}\n", "pointers cannot be stored in locals"},
		{"pointer field", "package sub\ntype S struct{ X *int32 }\n", "pointers cannot be stored in structs"},
		{"pointer result", "package sub\ntype S struct{ X int32 }\nfunc F(p *S) *S { return p }\n", "cannot return pointers"},
		{"pointer reassigned", "package sub\ntype S struct{ X int32 }\nfunc F(p *S, q *S) { p = q }\n", "pointer p cannot be reassigned"},
		{"function without a body", "package sub\nfunc F(x uint32) uint32\n", "function F has no body"},
		{"switch on a float", "package sub\nfunc F(x float32) uint32 {\n\tswitch x {\n\tcase 1:\n\t\treturn 1\n\t}\n\treturn 0\n}\n", "switch tag must be an integer or bool"},
		{"dereference of an address", "package sub\nfunc F() int32 {\n\tvar x int32\n\treturn *(&x)\n}\n", "dereference is allowed only of a pointer parameter"},
		{"struct conversion", "package sub\ntype S struct{ X int32 }\ntype T struct{ X int32 }\nfunc F(s S) T { return T(s) }\n", "conversions are allowed only to scalar types"},
		{"len of an array pointer", "package sub\nfunc F(p *[4]int32) int32 { return int32(len(p)) }\n", "len is allowed only on fixed-size arrays and on buffers (len)"},
		{"min of uint8", "package sub\nfunc F(a, b uint8) uint8 { return min(a, b) }\n", "min is allowed only on int32, int64, and uint32 operands"},
		{"call of a function parameter", "package sub\nfunc F(f func()) { f() }\n", "call of a non-function"},
		{"method expression", "package sub\ntype S struct{ X int32 }\nfunc (s S) m() int32 { return s.X }\nfunc F(s S) int32 { return S.m(s) }\n", "call of a field or method expression"},
		{"buffer from a call", "package sub\nimport \"example.com/compute\"\nfunc id(w compute.Variable) compute.Variable { return w }\nfunc h(w compute.Variable) {}\nfunc F(w compute.Variable) { h(id(w)) }\n", "a buffer is a parameter or a field of a buffer group parameter"},
		{"function field in a params row", "package sub\nimport \"example.com/compute\"\ntype P struct{ F func() }\nfunc Entry(w *compute.Words, p *P, x uint32) {}\n", "is not a buffer-visible type"},
		{"array of pointers", "package sub\nfunc F(a [2]*int32) {}\n", "pointer type outside a parameter"},
		{"struct holding a buffer and a scalar", "package sub\nimport \"example.com/compute\"\ntype S struct {\n\tW *compute.Words\n\tN int32\n}\n", "type S holds a buffer, so it is a buffer group"},
		{"NextPhase in a helper", "package sub\nimport \"example.com/compute\"\nfunc h() { compute.NextPhase() }\nfunc Entry(x, y, z uint32) { h() }\n", "NextPhase is called only in the body of a group entry point"},
		{"NextPhase through a dot import", "package sub\nimport . \"example.com/compute\"\nfunc Entry(x, y, z uint32) { NextPhase() }\n", "a compute package imports example.com/compute by name"},
		{"NextPhase in a method named like the group entry", "package sub\nimport \"example.com/compute\"\ntype S struct{ X uint32 }\nfunc (s *S) Entry(x, y, z uint32) { compute.NextPhase() }\nfunc Entry(x, y, z uint32) {}\n", "NextPhase is called only in the body of a group entry point"},
		{"NextPhase inside an if", "package sub\nimport \"example.com/compute\"\nfunc Entry(x, y, z uint32) {\n\tif x == 0 {\n\t\tcompute.NextPhase()\n\t}\n}\n", "NextPhase must be a statement directly in the group entry's body"},
		{"NextPhase inside a loop", "package sub\nimport \"example.com/compute\"\nfunc Entry(x, y, z uint32) {\n\tfor i := uint32(0); i < y; i++ {\n\t\tcompute.NextPhase()\n\t}\n}\n", "NextPhase must be a statement directly in the group entry's body"},
		{"NextPhase inside a switch", "package sub\nimport \"example.com/compute\"\nfunc Entry(x, y, z uint32) {\n\tswitch x {\n\tcase 0:\n\t\tcompute.NextPhase()\n\t}\n}\n", "NextPhase must be a statement directly in the group entry's body"},
		{"NextPhase inside a nested block", "package sub\nimport \"example.com/compute\"\nfunc Entry(x, y, z uint32) {\n\t{\n\t\tcompute.NextPhase()\n\t}\n}\n", "NextPhase must be a statement directly in the group entry's body"},
		{"group entry called", "package sub\nimport \"example.com/compute\"\nfunc Entry(x, y, z uint32) { compute.NextPhase() }\nfunc h() { Entry(1, 2, 3) }\n", "group entry point Entry is not called"},
		{"pointer to a buffer pointer", "package sub\nimport \"example.com/compute\"\ntype P struct{ A uint32 }\nfunc Entry(w **compute.Words, p *P, x uint32) {}\n", "parameter w: pointers may refer only to a buffer, a struct, or a scalar"},
		{"pointer to a struct pointer", "package sub\ntype S struct{ X int32 }\nfunc F(p **S) int32 { return (*p).X }\n", "parameter p: pointers may refer only to a buffer, a struct, or a scalar"},
		{"pointer to float64", "package sub\nfunc F(p *float64) {}\n", "type float64 is outside the compute subset"},
		{"pointer to int", "package sub\nfunc F(p *int) {}\n", "type int is outside the compute subset"},
		{"zero-length local array", "package sub\nfunc F() {\n\tvar a [0]int32\n\t_ = a\n}\n", "zero-length arrays are outside the compute subset"},
		{"zero-length array parameter", "package sub\nfunc F(a [0]int32) {}\n", "zero-length arrays are outside the compute subset"},
		{"zero-length array field", "package sub\ntype S struct{ A [0]uint32 }\n", "zero-length arrays are outside the compute subset"},
		{"zero-length table", "package sub\nvar t = [0]int32{}\n", "zero-length arrays are outside the compute subset"},
		{"read of a variable a call writes", "package sub\nfunc bump(p *uint32) uint32 { *p++; return 1 }\nfunc F(i uint32) uint32 { return i + bump(&i) }\n", "reads i outside its call to bump, which writes it"},
		{"compound target index a call writes", "package sub\nfunc bump(p *uint32) int32 { *p++; return 1 }\nfunc F(i uint32) int32 {\n\tvar a [4]int32\n\ta[i&3] += bump(&i)\n\treturn a[0]\n}\n", "reads i outside its call to bump, which writes it"},
		{"parallel target index a call writes", "package sub\nfunc bump(p *uint32) int32 { *p++; return 1 }\nfunc F(i uint32) int32 {\n\tvar a [4]int32\n\tvar b int32\n\ta[i&3], b = bump(&i), 0\n\treturn a[0] + b\n}\n", "reads i outside its call to bump, which writes it"},
		{"word compound index a call writes", "package sub\nimport \"example.com/compute\"\nfunc bump(p *uint32) uint32 { *p++; return 1 }\nfunc F(w compute.Variable, i uint32) {\n\tw[i] += bump(&i)\n}\n", "reads i outside its call to bump, which writes it"},
		{"read of a field a pointer method writes", "package sub\ntype C struct{ N int32 }\nfunc (c *C) inc() int32 { c.N++; return 1 }\nfunc F() int32 {\n\tvar c C\n\treturn c.N + c.inc()\n}\n", "reads c outside its call to inc, which writes it"},
		{"word read of a buffer a call takes", "package sub\nimport \"example.com/compute\"\nfunc put(w compute.Variable, i uint32) uint32 {\n\tw[i] = 5\n\treturn 0\n}\nfunc F(w compute.Variable, i uint32) uint32 { return w[i] + put(w, i) }\n", "indexes buffer w outside its call to put, which takes it"},
		{"word compound of a buffer a call takes", "package sub\nimport \"example.com/compute\"\nfunc put(w compute.Variable, i uint32) uint32 {\n\tw[i] = 5\n\treturn 0\n}\nfunc F(w compute.Variable, i uint32) {\n\tw[i] += put(w, i)\n}\n", "indexes buffer w outside its call to put, which takes it"},
		{"word read of a group buffer a call takes", "package sub\nimport \"example.com/compute\"\nfunc put(w compute.Variable, i uint32) uint32 {\n\tw[i] = 5\n\treturn 0\n}\nfunc F(g *compute.Pair, i uint32) uint32 { return g.Out[i] + put(g.Out, i) }\n", "indexes buffer g outside its call to put, which takes it"},
		{"buffer as a value", "package sub\nimport \"example.com/compute\"\nfunc F(w compute.Variable) { _ = w }\n", "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len"},
		{"dereferenced buffer as a value", "package sub\nimport \"example.com/compute\"\nfunc F(w *compute.Words) { _ = *w }\n", "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len"},
		{"group as a value", "package sub\nimport \"example.com/compute\"\nfunc F(g *compute.Pair) { _ = g }\n", "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len"},
		{"group field as a value", "package sub\nimport \"example.com/compute\"\nfunc F(g *compute.Pair) { _ = g.Out }\n", "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len"},
		{"group value receiver", "package sub\nimport \"example.com/compute\"\ntype G struct{ W *compute.Words }\nfunc (g G) M() {}\n", "fixed-size buffers and buffer groups are passed by pointer"},
		{"word read of a buffer an operation takes", "package sub\nimport \"example.com/compute\"\nfunc F(w compute.Variable, i uint32) uint32 { return w[i] + w.Add(i, 1) }\n", "indexes buffer w outside its call to Add, which takes it"},
		{"table copied", "package sub\nvar t = [2]int32{1, 2}\nfunc F() int32 {\n\tu := t\n\treturn u[0]\n}\n", "lookup table t is used only by indexing"},
		{"table as an argument", "package sub\nvar t = [2]int32{1, 2}\nfunc g(a [2]int32) int32 { return a[0] }\nfunc F() int32 { return g(t) }\n", "lookup table t is used only by indexing"},
		{"table in a composite literal", "package sub\nvar t = [2]int32{1, 2}\ntype S struct{ A [2]int32 }\nfunc F() int32 {\n\ts := S{A: t}\n\treturn s.A[0]\n}\n", "lookup table t is used only by indexing"},
		{"table returned", "package sub\nvar t = [2]int32{1, 2}\nfunc F() [2]int32 { return t }\n", "lookup table t is used only by indexing"},
		{"pointer method on a table element", "package sub\ntype C uint32\nfunc (c *C) inc() { *c++ }\nvar t = [2]C{1, 2}\nfunc F(x uint32) { t[x&1].inc() }\n", "address-of is allowed only"},
		{"reading pointer method on a table element", "package sub\ntype C uint32\nfunc (c *C) get() uint32 { return uint32(*c) }\nvar t = [2]C{1, 2}\nfunc F(x uint32) uint32 { return t[x&1].get() }\n", "address-of is allowed only"},
		{"pointer redeclared", "package sub\ntype S struct{ X int32 }\nfunc F(p *S, q *S) int32 {\n\tp, n := q, int32(1)\n\tp.X = n\n\treturn p.X\n}\n", "pointer p cannot be reassigned"},
		{"address of table", "package sub\nvar t = [2]int32{1, 2}\nfunc g(v *int32) {}\nfunc F() { g(&t[0]) }\n", "address-of is allowed only"},
		{"address outside call", "package sub\nfunc F() {\n\tvar x int32\n\t_ = &x\n}\n", "address-of is allowed only as a call argument"},
		{"import", "package sub\nimport \"fmt\"\nfunc F() { fmt.Println() }\n", "import \"fmt\" is outside the compute subset"},
		{"init", "package sub\nfunc init() {}\n", "init functions"},
		{"variadic", "package sub\nfunc F(a ...int32) {}\n", "variadic"},
		{"generic", "package sub\nfunc F[T any](a T) {}\n", "generic"},
		{"shift width", "package sub\nfunc F(x uint32) uint32 { return x << 32 }\n", "shift count 32 reaches the 32-bit width"},
		{"compound shift width", "package sub\nfunc F(x uint32) uint32 {\n\tx <<= 32\n\treturn x\n}\n", "shift count 32 reaches the 32-bit width"},
		{"struct comparison", "package sub\ntype S struct{ X int32 }\nfunc F() bool {\n\tvar a, b S\n\treturn a == b\n}\n", "comparison of non-scalar type"},
		{"array comparison", "package sub\nfunc F() bool {\n\tvar a, b [2]int32\n\treturn a != b\n}\n", "comparison of non-scalar type"},
		{"pointer comparison", "package sub\ntype S struct{ X int32 }\nfunc F(p, q *S) bool { return p == q }\n", "comparison of non-scalar type"},
		{"multi-result call as a statement", "package sub\nfunc g() (int32, int32) { return 1, 2 }\nfunc F() { g() }\n", "the results of a multi-result call must be assigned"},
		{"multi-result call spread", "package sub\nfunc g() (int32, int32) { return 1, 2 }\nfunc h(a, b int32) int32 { return a + b }\nfunc F() int32 { return h(g()) }\n", "a multi-result call cannot be spread as the arguments of a call"},
		{"tagless switch", "package sub\nfunc F(x int32) int32 {\n\tswitch {\n\tcase x < 0:\n\t\treturn 0\n\t}\n\treturn 1\n}\n", "switch must have an integer or bool tag"},
		{"non-constant case", "package sub\nfunc F(x, y int32) int32 {\n\tswitch x {\n\tcase y:\n\t\treturn 0\n\t}\n\treturn 1\n}\n", "switch cases must be constants"},
		{"break in switch", "package sub\nfunc F(x int32) {\n\tfor i := int32(0); i < 4; i++ {\n\t\tswitch x {\n\t\tcase 1:\n\t\t\tbreak\n\t\t}\n\t}\n}\n", "break inside a switch case"},
		{"fallthrough", "package sub\nfunc F(x int32) {\n\tswitch x {\n\tcase 1:\n\t\tfallthrough\n\tcase 2:\n\t}\n}\n", "fallthrough is outside"},
		{"goto", "package sub\nfunc F() {\nagain:\n\tgoto again\n}\n", "labels are outside"},
		{"package var", "package sub\nvar counter int32\n", "must be a lookup table"},
		{"table assigned", "package sub\nvar t = [2]int32{1, 2}\nfunc F() { t[0] = 3 }\n", "lookup table is read-only"},
		{"table of int", "package sub\nvar t = [2]int{1, 2}\n", "type int is outside the compute subset"},
		{"table element from a call", "package sub\nfunc g() int32 { return 1 }\nvar t = [2]int32{g(), 1}\n", "lookup table t element must be a constant"},
		{"table element from a table", "package sub\nvar u = [2]int32{1, 2}\nvar t = [2]int32{1: u[0]}\n", "lookup table t element must be a constant"},
		{"string constant", "package sub\nfunc F() { _ = \"x\" }\n", "constant of type string is outside the compute subset"},
		{"float64 constant", "package sub\nfunc F() { _ = 1.5 }\n", "constant of type float64 is outside the compute subset"},
		{"unnamed struct local", "package sub\nfunc F() int32 {\n\tvar x struct{ A int32 }\n\treturn x.A\n}\n", "unnamed struct type is outside the compute subset"},
		{"unnamed struct parameter", "package sub\nfunc F(p *struct{ A int32 }) int32 { return p.A }\n", "pointers may refer only to a buffer, a struct, or a scalar"},
		{"unnamed struct literal", "package sub\nfunc F() int32 { return struct{ A int32 }{1}.A }\n", "unnamed struct type is outside the compute subset"},
		{"group judged after a later group", "package sub\nimport \"example.com/compute\"\ntype A struct {\n\tZ Z\n\tN int32\n}\ntype Z struct{ W *compute.Words }\n", "type A holds a buffer, so it is a buffer group"},
		{"embedded field", "package sub\ntype A struct{ X int32 }\ntype B struct{ A }\n", "embedded fields"},
		{"slice", "package sub\nfunc F(a [4]int32) int32 { return a[1:][0] }\n", "slicing is outside"},
		{"map", "package sub\nfunc F() {\n\tvar m map[int32]int32\n\t_ = m\n}\n", "outside the compute subset"},
		{"builtin", "package sub\nfunc F() { panic(1) }\n", "builtin panic is outside"},
		{"len of non-array", "package sub\nfunc F(s string) int32 { return int32(len(s)) }\n", "type string is outside"},
		{"defer", "package sub\nfunc g() {}\nfunc F() { defer g() }\n", "DeferStmt is outside"},
		{"method value", "package sub\ntype S struct{ X int32 }\nfunc (s *S) m() {}\nfunc F(s *S) { _ = s.m }\n", "method values"},
		{"type error", "package sub\nfunc F() int32 { return undefined }\n", "undefined: undefined"},
		{"build tag", "//go:build ignore\n\npackage sub\n", "build constraints"},
		{"entry missing", "package sub\nfunc entry(x uint32) {}\n", "entry point Entry is not an exported function"},
		{"entry by value", "package sub\ntype P struct{ A uint32 }\nfunc Entry(p P, x uint32) {}\n", "must be a buffer, a struct pointer, or an int32/uint32 coordinate"},
		{"entry pointer to scalar", "package sub\nfunc Entry(p *int32, x uint32) {}\n", "must be a buffer, a struct pointer, or an int32/uint32 coordinate"},
		{"entry result", "package sub\nfunc Entry(x uint32) uint32 { return x }\n", "must not return values"},
		{"entry no coords", "package sub\ntype P struct{ A uint32 }\nfunc Entry(p *P) {}\n", "must end with one to three int32/uint32 coordinates"},
		{"entry buffer after struct", "package sub\nimport \"example.com/compute\"\ntype P struct{ A uint32 }\nfunc Entry(p *P, b *compute.Buf, x uint32) {}\n", "buffers must precede the params struct"},
		{"buffer-visible narrow", "package sub\ntype P struct{ N [3]uint16 }\nfunc Entry(p *P, x uint32) {}\n", "must be a bool or a 32-bit or 64-bit scalar"},
		{"params row written", "package sub\ntype P struct{ A uint32 }\nfunc Entry(p *P, x uint32) { p.A = x }\n", "writes through p, the params row of entry point Entry"},
		{"params row field written", "package sub\ntype P struct{ A [2]uint32 }\nfunc Entry(p *P, x uint32) { p.A[0] = x }\n", "writes through p, the params row of entry point Entry"},
		{"params row replaced", "package sub\ntype P struct{ A uint32 }\nfunc Entry(p *P, x uint32) {\n\tvar q P\n\t*p = q\n}\n", "writes through p, the params row of entry point Entry"},
		{"params row written by a callee", "package sub\ntype P struct{ A uint32 }\nfunc bump(q *P) { q.A++ }\nfunc Entry(p *P, x uint32) { bump(p) }\n", "writes through q, the params row of entry point Entry"},
		{"params row written by a method", "package sub\ntype P struct{ A uint32 }\nfunc (q *P) bump() { q.A++ }\nfunc Entry(p *P, x uint32) { p.bump() }\n", "writes through q, the params row of entry point Entry"},
		{"params row field address passed", "package sub\ntype P struct{ A [2]uint32 }\nfunc bump(v *uint32) { *v++ }\nfunc Entry(p *P, x uint32) { bump(&p.A[x&1]) }\n", "writes through v, the params row of entry point Entry"},
		{"params row field as a receiver", "package sub\ntype I struct{ N uint32 }\ntype P struct{ In I }\nfunc (i *I) bump() { i.N++ }\nfunc Entry(p *P, x uint32) { p.In.bump() }\n", "writes through i, the params row of entry point Entry"},
		{"structs in the other order", "package sub\ntype P struct{ A uint32 }\ntype S struct{ N uint32 }\nfunc Entry(s *S, p *P, x uint32) { s.N += p.A }\n", "writes through s, the params row of entry point Entry"},
		{"overlapping references, whole and field", "package sub\ntype I struct{ N uint32 }\ntype S struct{ X uint32; In I }\nfunc f(s *S, i *I) { i.N = s.X }\nfunc Entry(x uint32) {\n\tvar s S\n\tf(&s, &s.In)\n}\n", "arguments 1 and 2 of the call to f reference overlapping storage of s"},
		{"overlapping references, same element", "package sub\ntype S struct{ X uint32 }\nfunc f(a *S, b *S) { a.X = b.X }\nfunc Entry(x uint32) {\n\tvar s [2]S\n\tf(&s[x&1], &s[1])\n}\n", "arguments 1 and 2 of the call to f reference overlapping storage of s"},
		{"overlapping references, receiver and argument", "package sub\ntype S struct{ X uint32 }\nfunc (a *S) m(b *S) { a.X = b.X }\nfunc Entry(x uint32) {\n\tvar s S\n\ts.m(&s)\n}\n", "arguments 0 and 1 of the call to m reference overlapping storage of s"},
		{"overlapping references, written by a callee", "package sub\ntype S struct{ X uint32 }\nfunc g(a *S) { a.X++ }\nfunc f(a *S, b *S) { g(a) }\nfunc Entry(x uint32) {\n\tvar s S\n\tf(&s, &s)\n}\n", "arguments 1 and 2 of the call to f reference overlapping storage of s"},
		{"overlapping references, pointer parameter passed on", "package sub\ntype I struct{ N uint32 }\ntype S struct{ X uint32; In I }\nfunc f(s *S, i *I) { i.N = s.X }\nfunc g(p *S) { f(p, &p.In) }\nfunc Entry(x uint32) {\n\tvar s S\n\tg(&s)\n}\n", "arguments 1 and 2 of the call to f reference overlapping storage of p"},
		{"overlapping references, written through a field passed on", "package sub\ntype I struct{ N uint32 }\ntype S struct{ X uint32; In I }\nfunc w(n *uint32) { *n = 1 }\nfunc f(s *S, i *I) { w(&s.In.N) }\nfunc Entry(x uint32) {\n\tvar s S\n\tf(&s, &s.In)\n}\n", "arguments 1 and 2 of the call to f reference overlapping storage of s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := load(t, c.src, "Entry")
			if err == nil {
				t.Fatalf("accepted; want %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got:\n%s\nwant substring %q", err, c.want)
			}
		})
	}
}

func TestParentRules(t *testing.T) {
	sub := "package sub\n"
	cases := []struct {
		name, parent, want string
	}{
		{"import", "package compute\nimport \"fmt\"\nvar _ = fmt.Sprint\n", "import \"fmt\" is outside the compute subset"},
		{"struct type", "package compute\ntype S struct{ X int32 }\n", "is not a byte or word buffer type or a buffer group"},
		{"group with a fixed buffer by value", "package compute\ntype V []uint32\ntype W [4]uint32\ntype G struct{ A V; B W }\n", "type G is not a byte or word buffer type or a buffer group"},
		{"group with a scalar", "package compute\ntype V []uint32\ntype G struct{ A V; N int32 }\n", "type G is not a byte or word buffer type or a buffer group"},
		{"group of itself", "package compute\ntype V []uint32\ntype G struct{ A V; B *G }\n", "type G is not a byte or word buffer type or a buffer group"},
		{"empty group", "package compute\ntype G struct{}\n", "type G is not a byte or word buffer type or a buffer group"},
		{"halfword slice", "package compute\ntype S []uint16\n", "is not a byte or word buffer type"},
		{"package var", "package compute\nvar n int32\n", "no package-level variables"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(
				Source{Path: testParentPath, Files: fstest.MapFS{"p.go": {Data: []byte(c.parent)}}},
				Source{Path: testParentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(sub)}}},
			)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want substring %q", err, c.want)
			}
		})
	}
}

// TestVariableBufferRules holds a variable-length buffer to its
// uses: indexed, its length converted, the receiver of a helper, and
// passed on by value. It is never built, reassigned, sliced, or
// grown.
func TestVariableBufferRules(t *testing.T) {
	const prefix = "package sub\nimport \"example.com/compute\"\nfunc h(v compute.Variable) {}\n"
	valid := prefix + "func F(b compute.Variable, i uint32) {\n\tn := b.Add(0, 1)\n\tb[i] = b[i&(uint32(len(b))-1)] + n\n\tb[i]++\n\tb[0] = uint32((len(b)))\n\th(b)\n}\n"
	if err := load(t, valid, "F"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ body, want string }{
		{"var x compute.Variable; _ = x", "buffers appear only as parameters"},
		{"b = b", "buffers cannot be reassigned"},
		{"b, n := b, int32(0); _ = n", "buffers cannot be reassigned"},
		{"b = append(b, 1)", "outside the compute subset"},
		{"_ = b[:1]", "slicing is outside the compute subset"},
		{"_ = make(compute.Variable, 8)", "outside the compute subset"},
		{"_ = len(b)", "the length of a buffer is used as the operand of a conversion"},
	} {
		src := prefix + "func F(b compute.Variable) { " + c.body + " }"
		if err := load(t, src); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %s", c.body, err, c.want)
		}
	}
}

// TestVariableByteBufferRules holds a variable-length byte buffer to
// its uses: the receiver of its accessors, its length converted, and
// passed on by value. Like a fixed byte buffer it is never indexed.
func TestVariableByteBufferRules(t *testing.T) {
	const prefix = "package sub\nimport \"example.com/compute\"\nfunc h(v compute.Bytes) {}\n"
	valid := prefix + "func F(b compute.Bytes, i uint32) {\n\tb.PutU16BE(i&(uint32(len(b))-1), b.U16BE(i)+1)\n\th(b)\n}\n"
	if err := load(t, valid, "F"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ body, want string }{
		{"_ = b[0]", "byte buffer Bytes is read and written through its accessors"},
		{"b[0] = 1", "byte buffer Bytes is read and written through its accessors"},
		{"b = b", "buffers cannot be reassigned"},
		{"_ = b[:1]", "slicing is outside the compute subset"},
	} {
		src := prefix + "func F(b compute.Bytes) { " + c.body + " }"
		if err := load(t, src); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %s", c.body, err, c.want)
		}
	}
	if err := load(t, "package sub\nimport \"example.com/compute\"\nfunc F(b *compute.Bytes) {}\n"); err == nil || !strings.Contains(err.Error(), "a variable-length buffer is passed by value") {
		t.Errorf("byte buffer by pointer: got %v", err)
	}
}

// groupSub declares buffer groups in a compute subpackage: a group of
// a parent group, a nested group, an array of fixed-size buffers, and
// a byte buffer, and the helpers the rules below pass their buffers
// to.
const groupSub = `package sub

import "example.com/compute"

type Inner struct {
	W *compute.Words
	V compute.Variable
}

type G struct {
	P  *compute.Pair
	In *Inner
	Ws [2]*compute.Words
	B  *compute.Buf
}

func h(v compute.Variable)  {}
func hw(w *compute.Words)   {}
func hp(p *compute.Pair)    {}
func g2(g *G)               {}
`

// TestBufferGroupRules holds a buffer group to its uses: passed whole
// or by a field to a function, a field indexed or the receiver of a
// helper, and an array of buffers chosen by a variable only to read a
// word as the whole value of an assignment. Every other use of the
// group or a field is rejected.
func TestBufferGroupRules(t *testing.T) {
	valid := groupSub + `
func F(g *G, k, i uint32) {
	g.P.Out[i] = g.P.In[i] + uint32(len(g.P.In))
	h(g.In.V)
	hw(g.Ws[1])
	hp(g.P)
	x := g.Ws[k][i]
	g.Ws[0][i] = x + uint32(g.B.U8(i)) + g.In.V.Add(0, 1)
	g2(g)
}
`
	if err := load(t, valid, "F"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ body, want string }{
		{"_ = g.In", "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len"},
		{"g.In = g.In", "buffers cannot be reassigned"},
		{"*g = *g", "buffers cannot be reassigned"},
		{"var x G; _ = x", "buffers appear only as parameters"},
		{"hw(g.Ws[k])", "an array of buffers is indexed by a variable only to read a word"},
		{"x := g.Ws[k][i] + 1; _ = x", "an array of buffers is indexed by a variable only to read a word"},
		{"g.Ws[k][i] = 1", "an array of buffers is indexed by a variable only to read a word"},
		{"_ = g.B[i]", "byte buffer Buf is read and written through its accessors"},
	} {
		src := groupSub + "func F(g *G, k, i uint32) { " + c.body + " }"
		if err := load(t, src); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %s", c.body, err, c.want)
		}
	}
}

// TestConstantKinds accepts the constant kinds the lowering takes: a
// rune literal, an untyped shift count, an index constant (typed int
// by go/types), and a literal converted to float32.
func TestConstantKinds(t *testing.T) {
	src := "package sub\nfunc F(x uint32, arr [4]int32) float32 {\n\tc := uint32('a')\n\tx <<= 3\n\treturn float32(1.5) * float32(arr[1]+int32(c)+int32(x))\n}\n"
	if err := load(t, src); err != nil {
		t.Fatal(err)
	}
}

// TestBufferGroupOrder resolves a group whose only buffer field points
// to a group declared, and sorted, after it.
func TestBufferGroupOrder(t *testing.T) {
	src := "package sub\nimport \"example.com/compute\"\ntype A struct{ B *B }\ntype B struct{ W *compute.Words }\nfunc Entry(a *A, i uint32) { a.B.W[i] = i }\n"
	if err := load(t, src, "Entry"); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsSortedAndComplete(t *testing.T) {
	src := "package sub\nfunc F() {\n\tfor i := 0; i < 1; i++ {\n\t}\n\tvar f float64\n\t_ = f\n}\n"
	err := load(t, src)
	list, ok := err.(ErrorList)
	if !ok || len(list) < 2 {
		t.Fatalf("got %v, want an ErrorList with the int and float64 diagnostics", err)
	}
	for i := 1; i < len(list); i++ {
		if list[i].Pos.Line < list[i-1].Pos.Line {
			t.Fatalf("diagnostics not sorted: %v", list)
		}
	}
}
