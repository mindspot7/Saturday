// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// lower runs the frontend and the lowering on one subpackage source
// over the test parent and returns the module dump.
func lower(t *testing.T, sub string, entries ...string) string {
	t.Helper()
	parent := fstest.MapFS{"buffers.go": {Data: []byte(testParent)}}
	subFS := fstest.MapFS{"sub.go": {Data: []byte(sub)}}
	prog, err := Load(
		Source{Path: testParentPath, Files: parent},
		Source{Path: testParentPath + "/sub", Files: subFS, Entries: entries},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	return mod.Dump()
}

// TestLowerConstantWidth holds an implementation-sized constant, which
// lowers to 32 bits, to a value that fits them: a range bound past
// int32 fails, one at its maximum lowers, and a wide constant that
// only a 64-bit conversion reads lowers at the conversion's width.
func TestLowerConstantWidth(t *testing.T) {
	parent := fstest.MapFS{"buffers.go": {Data: []byte(testParent)}}
	lowerSub := func(sub string) error {
		prog, err := Load(
			Source{Path: testParentPath, Files: parent},
			Source{Path: testParentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(sub)}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Lower(prog)
		return err
	}
	const want = "constant 5000000000 does not fit the 32-bit integer int lowers to"
	if err := lowerSub("package sub\nfunc F() {\n\tfor range 5000000000 {\n\t}\n}\n"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("range past int32: got %v, want %q", err, want)
	}
	if err := lowerSub("package sub\nfunc F() {\n\tfor range 2147483647 {\n\t}\n}\n"); err != nil {
		t.Errorf("range at the int32 maximum: %v", err)
	}
	if err := lowerSub("package sub\nconst wide = 1 << 40\nfunc F() uint64 { return uint64(wide) + 1 }\n"); err != nil {
		t.Errorf("wide constant read at 64 bits: %v", err)
	}
}

// TestLowerEvaluationOrder pins Go's left-to-right evaluation of
// calls: when a later operand is a short-circuit rewrite or holds a
// call, the calls in an earlier operand are evaluated into
// temporaries ahead of the if statement the rewrite emits or of the
// later call, at every site that lowers operands in sequence, with
// the operand keeping its shape around them. The wanted strings must
// appear in the dump in the order listed.
func TestLowerEvaluationOrder(t *testing.T) {
	const helpers = `type S struct {
	X int32
	Y bool
}
func g(x int32) int32 { return x + 1 }
func f(x int32) bool { return x > 1 }
func h(x int32, b bool) int32 { return x }
func k(b bool) int32 { return 1 }
func u(b bool) uint32 { return 1 }
func w(v *int32, b bool) { *v = *v + 1 }
func two(a, b int32) (int32, int32) { return a, b }
func word(a int32) (uint32, int32) { return uint32(a), a }
`
	cases := []struct {
		name, src string
		want      []string
	}{
		{name: "call arguments", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	return h(g(a), a > 0 && f(a))
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "return h(t"}},
		{name: "method receiver", src: "package sub\n" + helpers + `
func (s *S) m(b bool) int32 { return s.X }
func F(a int32) int32 {
	var s S
	return s.m(a > 0 && f(a))
}`, want: []string{"if t", "= f(a#0)", "return S.m(s#"}},
		{name: "binary operands", src: "package sub\n" + helpers + `
func F(a int32) bool {
	return g(a) > 0 == (a > 0 && f(a))
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "return ((t"}},
		{name: "min operands", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	return min(g(a), k(a > 0 && f(a)))
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "return op.MinI32(t"}},
		{name: "composite elements", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	s := S{X: g(a), Y: a > 0 && f(a)}
	return s.X
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "struct#0{t"}},
		{name: "index operands", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	return arr[uint32(g(a))&3] + k(a > 0 && f(a))
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "return (arr#1[(u32(t"}},
		{name: "return values", src: "package sub\n" + helpers + `
func F(a int32) (int32, bool) {
	return g(a), a > 0 && f(a)
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "return t"}},
		{name: "store index", src: "package sub\nimport \"example.com/compute\"\n" + helpers + `
func F(w *compute.Words, a int32) {
	w[uint32(g(a))] = u(a > 0 && f(a))
}`, want: []string{"= g(a#1)", "if t", "= f(a#1)", "op.Store(w#0, u32(t"}},
		{name: "nested call", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	return h(g(a), f(k(a > 0 && f(a))))
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "return h(t"}},
		{name: "pure right operand stays inline", src: "package sub\n" + helpers + `
func F(a, b int32) int32 {
	return h(g(a), a > 0 && b > 0)
}`, want: []string{"var t2 i32 = g(a#0)", "return h(t2, ((a#0 > 0) && (b#1 > 0)))"}},
		{name: "reference argument keeps its path", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	w(&arr[uint32(g(a))&3], a > 0 && f(a))
	return arr[0]
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "w(arr#1[(u32(t"}},
		{name: "short-circuit right operand calls", src: "package sub\n" + helpers + `
func F(a int32) bool {
	return a > 0 && g(a) > g(a+1)
}`, want: []string{"if t", "= g(a#0)", "= (t", "> g((a#0 + 1)))"}},
		{name: "multi-result call arguments", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	x, y := two(g(a), g(a+1))
	return x + y
}`, want: []string{"var t1 i32 = g(a#0)", "var t2 i32 = g((a#0 + 1))", "x#3, y#4 = two(t1, t2)"}},
		{name: "multi-result call forwarded", src: "package sub\n" + helpers + `
func F(a int32) (int32, int32) {
	return two(g(a), g(a+1))
}`, want: []string{"var t1 i32 = g(a#0)", "var t2 i32 = g((a#0 + 1))", "t3, t4 = two(t1, t2)", "return t3, t4"}},
		{name: "multi-result targets before the call", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	var y int32
	arr[uint32(g(a))&3], y = two(a, a)
	return arr[0] + y
}`, want: []string{"= (u32(g(a#0)) & 3)", "arr#1[t", "= two(a#0, a#0)"}},
		{name: "assign target before the value's call", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	arr[uint32(g(a))&3] = g(a + 1)
	return arr[0]
}`, want: []string{"= g(a#0)", "arr#1[(u32(t", "= g((a#0 + 1))"}},
		{name: "assign target calls but the last stay", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	arr[uint32(g(a)+g(a+1))&3] = 5
	return arr[0]
}`, want: []string{"= g(a#0)", "arr#1[(u32((t", " + g((a#0 + 1)))) & 3)] = 5"}},
		{name: "compound target before the value's statements", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	arr[uint32(g(a))&3] += k(a > 0 && f(a))
	return arr[0]
}`, want: []string{"= (u32(g(a#0)) & 3)", "if t", "= f(a#0)", "arr#1[t"}},
		{name: "word target index before the call", src: "package sub\nimport \"example.com/compute\"\n" + helpers + `
func F(w *compute.Words, a int32) int32 {
	var y int32
	w[uint32(g(a))], y = word(a)
	return y
}`, want: []string{"= u32(g(a#1))", "= word(a#1)", "op.Store(w#0, t"}},
		{name: "parallel assign targets before the values", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var arr [4]int32
	var y int32
	arr[uint32(g(a))&3], y = g(a+1), g(a+2)
	return arr[0] + y
}`, want: []string{"= (u32(g(a#0)) & 3)", "= g((a#0 + 1))", "= g((a#0 + 2))", "arr#1[t"}},
		{name: "word compound with a call index and a short-circuit value", src: "package sub\nimport \"example.com/compute\"\n" + helpers + `
func F(w *compute.Words, a int32) {
	w[uint32(g(a))] += u(a > 0 && f(a))
}`, want: []string{"= u32(g(a#1))", "if t", "= f(a#1)", "= op.Load(w#0, t", "op.Store(w#0, t"}},
		{name: "word increment with a call index", src: "package sub\nimport \"example.com/compute\"\n" + helpers + `
func F(w *compute.Words, a int32) {
	w[uint32(g(a))]++
}`, want: []string{"var t2 i32 = g(a#1)", "var t3 u32 = op.Load(w#0, u32(t2))", "op.Store(w#0, u32(t2), (t3 + 1))"}},
		{name: "for condition with a nested call in the right operand", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	n := int32(0)
	for i := a; f(i) && f(g(k(f(i)))); i++ {
		n += i
	}
	return n
}`, want: []string{"for ; ; i#2 = (i#2 + 1) {", "var t6 bool = f(i#2)", "if t6 {", "var t3 bool = f(i#2)", "var t4 i32 = k(t3)", "var t5 i32 = g(t4)", "t6 = f(t5)", "if (! t6) {", "break"}},
		{name: "for post with a short-circuit call", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	n := int32(0)
	for i := a; i < 4; i = k(g(i) > 0 && f(i)) {
		if i == 2 {
			continue
		}
		n += i
	}
	return n
}`, want: []string{"for ; (i#2 < 4);  {", "if (i#2 == 2) {", "var t3 bool = (g(i#2) > 0)", "if t3 {", "t3 = f(i#2)", "i#2 = k(t3)", "continue", "n#1 = (n#1 + i#2)", "var t3 bool = (g(i#2) > 0)", "if t3 {", "t3 = f(i#2)", "i#2 = k(t3)"}},
		{name: "multi-result call as a for post", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	s := int32(0)
	for i, j := a, a; i < 4; i, j = two(i, j) {
		s += j
	}
	return s
}`, want: []string{"for ; (i#4 < 4); i#4, j#5 = two(i#4, j#5) {"}},
		{name: "nested target index before a short-circuit index", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var m [2][2]int32
	m[uint32(g(a))&1][uint32(k(a > 0 && f(a)))&1] = 1
	return m[0][0]
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "m#1[(u32(t", "[(u32(k(t"}},
		{name: "nested reference argument index before a short-circuit index", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var m [2][2]int32
	w(&m[uint32(g(a))&1][uint32(k(a > 0 && f(a)))&1], true)
	return m[0][0]
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "= k(t", "w(m#1[(u32(t"}},
		{name: "nested compound target index before a short-circuit index", src: "package sub\n" + helpers + `
func F(a int32) int32 {
	var m [2][2]int32
	m[uint32(g(a))&1][uint32(k(a > 0 && f(a)))&1] += 1
	return m[0][0]
}`, want: []string{"= g(a#0)", "if t", "= f(a#0)", "= k(t", "m#1[(u32(t"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dump := lower(t, c.src)
			pos := 0
			for _, want := range c.want {
				i := strings.Index(dump[pos:], want)
				if i < 0 {
					t.Fatalf("missing %q after offset %d in %s", want, pos, dump)
				}
				pos += i + len(want)
			}
		})
	}
}

func TestLowerValidSubset(t *testing.T) {
	dump := lower(t, validSub, "Entry")
	for _, want := range []string{
		"struct#0 Params size=32 align=8",
		"Base u32 @0",
		"Ratio [2]i32 @4",
		"Inner struct#1 @16",
		"On bool @24",
		"table#0 table [4]u32",
		"func#0 stepper.step(&s#0 struct#3)",
		"func#1 stepper.value(s#0 struct#3) i32",
		"entry Entry coords=2 buffer0=Buf buffer1=Words params=p params=st",
		"var t4 i32 = stepper.value(st2#3)",
		"(op.MinI32(t4, p#0.Ratio[(i#2 & 1)]) + abs32(i32(table[(i#2 & 3)])))",
		"table[(i#2 & 3)]",
		"op.U8(in#0, x#4)",
		"addr(n#8)",
		"addr(st#3.Pos)",
		"u32(i8(b#7))",
		"var k#12 i32 = t11",
		"if p#2.On {",
		"op.Store(out#1, (x#4 + y#5), acc#6)",
		"var t13 u32 = op.Load(out#1, x#4)",
		"op.Store(out#1, x#4, (t13 + acc#6))",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump lacks %q\n%s", want, dump)
		}
	}
}

// TestLowerVariableBuffer lowers a variable-length buffer passed by
// value: an index is a load or a store, a compound assignment both,
// and len the bound length.
func TestLowerVariableBuffer(t *testing.T) {
	dump := lower(t, `package sub
import "example.com/compute"
func helper(b compute.Variable, i uint32) {
	b[i] = b[i] + uint32(len(b))
	b[i] |= 2
	b[0] = uint32((len(b)))
}
func Entry(b compute.Variable, i uint32) { if i < uint32(len(b)) { helper(b, i) } }
`, "Entry")
	for _, want := range []string{
		"buffer0=Variable",
		"var t2 u32 = op.Load(b#0, i#1)",
		"op.Store(b#0, i#1, (t2 + op.Length(b#0)))",
		"var t3 u32 = op.Load(b#0, i#1)",
		"op.Store(b#0, i#1, (t3 | 2))",
		"op.Store(b#0, 0, op.Length(b#0))",
		"if (i#1 < op.Length(b#0))",
		"helper(b#0, i#1)",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q in %s", want, dump)
		}
	}
}

// TestLowerVariableByteBuffer lowers a variable-length byte buffer
// passed by value: its accessors are the byte helpers on the buffer,
// len the bound length, and its binding a dynamic byte buffer.
func TestLowerVariableByteBuffer(t *testing.T) {
	const sub = `package sub
import "example.com/compute"
func helper(b compute.Bytes, i uint32) { b.PutU16BE(i, b.U16BE(i+2)) }
func Entry(b compute.Bytes, i uint32) { if i < uint32(len(b)) { helper(b, i) } }
`
	prog, err := Load(
		Source{Path: testParentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(testParent)}}},
		Source{Path: testParentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(sub)}}, Entries: []string{"Entry"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	if bt := mod.Entries[0].Buffers[0].Type; bt.Name != "Bytes" || bt.Word || !bt.Dynamic || bt.Kind() != ByteBufferKind {
		t.Errorf("binding type %+v, want a dynamic byte buffer", bt)
	}
	dump := mod.Dump()
	for _, want := range []string{
		"buffer0=Bytes",
		"var t2 u16 = op.U16BE(b#0, (i#1 + 2))",
		"op.PutU16BE(b#0, i#1, t2)",
		"if (i#1 < op.Length(b#0))",
		"helper(b#0, i#1)",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q in %s", want, dump)
		}
	}
}

// TestLowerBufferGroup lowers a buffer group parameter to one buffer
// parameter per buffer it holds, depth first in field order: the
// entry binds them in that order, a path to a buffer is that buffer's
// parameter, passing the group or a nested group on passes its
// buffers, and a word of an array of buffers chosen by a variable is
// read by a switch over the array's buffers.
func TestLowerBufferGroup(t *testing.T) {
	dump := lower(t, groupSub+`
func helper(p *compute.Pair, i uint32) { p.Out[i] = p.In[i] + uint32(len(p.In)) }
func Entry(g *G, k, i uint32) {
	helper(g.P, i)
	hw(g.Ws[1])
	x := g.Ws[k][i]
	g.In.W[i] = x + uint32(g.B.U8(i))
	g2(g)
}
`, "Entry")
	for _, want := range []string{
		"buffer0=Variable buffer1=Variable buffer2=Words buffer3=Variable buffer4=Words buffer5=Words buffer6=Buf",
		"op.Store(p.Out#1, i#2, (t3 + op.Length(p.In#0)))",
		"helper(g.P.In#0, g.P.Out#1, i#8)",
		"hw(g.Ws[1]#5)",
		"switch t9 {",
		"t11 = op.Load(g.Ws[0]#4, t10)",
		"t11 = op.Load(g.Ws[1]#5, t10)",
		"var x#12 u32 = t11",
		"op.Store(g.In.W#2, i#8, (x#12 + u32(t13)))",
		"g2(g.P.In#0, g.P.Out#1, g.In.W#2, g.In.V#3, g.Ws[0]#4, g.Ws[1]#5, g.B#6)",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q in %s", want, dump)
		}
	}
}

// TestLowerRange lowers a range over an integer to a counted loop on
// a hidden counter with the key a fresh local set from it, so the
// body's assignment to the key does not change the iterations, and
// the bound evaluated once.
func TestLowerRange(t *testing.T) {
	dump := lower(t, `package sub
func F(n int32) int32 {
	s := int32(0)
	for i := range n {
		i += 10
		s += i
	}
	for range 3 {
		s++
	}
	return s
}
`)
	for _, want := range []string{
		"var t2 i32 = n#0",
		"var t3 i32 = 0",
		"for ; (t3 < t2); t3 = (t3 + 1) {",
		"var i#4 i32 = t3",
		"i#4 = (i#4 + 10)",
		"var t5 i32 = 3",
		"for ; (t6 < t5); t6 = (t6 + 1) {",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q in %s", want, dump)
		}
	}
}

func TestLowerRules(t *testing.T) {
	cases := []struct {
		name, src string
		entries   []string
		want      []string
	}{
		{name: "narrow mask at store", src: `package sub
func F(a, b uint8) uint8 {
	c := a + b
	return c
}`, want: []string{"var c#2 u8 = mask.u8((a#0 + b#1))", "return c#2"}},
		{name: "narrow mask at compare and div", src: `package sub
func F(a, b int16) bool {
	return (a*b)/2 == a-b
}`, want: []string{"(mask.i16((a#0 * b#1)) / 2)", "== mask.i16((a#0 - b#1))"}},
		{name: "narrow div masked", src: `package sub
func F(a, b int8) int8 {
	if b == 0 {
		b = 1
	}
	return a / b
}`, want: []string{"return mask.i8((a#0 / b#1))"}},
		{name: "bitwise canonical", src: `package sub
func F(a, b uint8) uint8 {
	return a & b
}`, want: []string{"return (a#0 & b#1)"}},
		{name: "wide no mask", src: `package sub
func F(a, b int32) int32 {
	return a*b + 1
}`, want: []string{"return ((a#0 * b#1) + 1)"}},
		{name: "short circuit rewrite", src: `package sub
func g(x int32) bool { return x > 0 }
func F(a, b int32) bool {
	return a > 0 && g(b)
}`, want: []string{"var t2 bool = (a#0 > 0)", "if t2 {", "t2 = g(b#1)", "return t2"}},
		{name: "short circuit or", src: `package sub
func g(x int32) bool { return x > 0 }
func F(a, b int32) bool {
	return a > 0 || g(b)
}`, want: []string{"if (! t2) {", "t2 = g(b#1)"}},
		{name: "short circuit pure", src: `package sub
func F(a, b int32) bool {
	return a > 0 && b > 0
}`, want: []string{"return ((a#0 > 0) && (b#1 > 0))"}},
		{name: "evaluation order temps", src: `package sub
func g(x int32) int32 { return x + 1 }
func F(a int32) int32 {
	return g(a) - g(a+1)
}`, want: []string{"var t1 i32 = g(a#0)", "return (t1 - g((a#0 + 1)))"}},
		{name: "method receiver ref", src: `package sub
type S struct{ X int32 }
func (s *S) inc() { s.X++ }
func F() int32 {
	var s S
	s.inc()
	return s.X
}`, want: []string{"func#0 S.inc(&s#0 struct#0)", "s#0.X = (s#0.X + 1)", "S.inc(s#0)"}},
		{name: "multi result", src: `package sub
func two(a int32) (int32, bool) { return a, a > 0 }
func F(a int32) int32 {
	x, ok := two(a)
	if ok {
		return x
	}
	_, ok = two(a)
	return 0
}`, want: []string{"func#0 two(a#0 i32) i32 bool", "x#1, ok#2 = two(a#0)", "t3, ok#2 = two(a#0)"}},
		{name: "named results bare return", src: `package sub
func F(a int32) (x, y int32) {
	x = a
	if a > 0 {
		return
	}
	y = 1
	return
}`, want: []string{"var x#1 i32 = 0", "var y#2 i32 = 0", "return x#1, y#2"}},
		{name: "parallel assign index before the stores", src: `package sub
func F(a int32) int32 {
	var x [2]int32
	i := int32(0)
	i, x[i] = 1, 2
	return x[0] + i
}`, want: []string{"var t3 i32 = i#2", "var t4 i32 = 1", "var t5 i32 = 2", "i#2 = t4", "x#1[t3] = t5"}},
		{name: "parallel assign word index before the stores", src: `package sub
import "example.com/compute"
func F(w *compute.Words) {
	i := uint32(0)
	i, w[i] = 1, 2
}`, want: []string{"var t2 u32 = i#1", "i#1 = t3", "op.Store(w#0, t2, t4)"}},
		{name: "multi-result assign index before the stores", src: `package sub
func two(a, b int32) (int32, int32) { return a, b }
func F(a int32) int32 {
	var x [2]int32
	i := int32(0)
	i, x[i] = two(1, 2)
	return x[0] + i
}`, want: []string{"var t3 i32 = i#2", "i#2, x#1[t3] = two(1, 2)"}},
		{name: "blank named result", src: `package sub
func F(a int32) (x int32, _ int32) {
	x = a
	return
}`, want: []string{"var x#1 i32 = 0", "var t2 i32 = 0", "return x#1, t2"}},
		{name: "all results blank, bare return", src: `package sub
func F() (_ int32, _ bool) {
	return
}`, want: []string{"var t0 i32 = 0", "var t1 bool = false", "return t0, t1"}},
		{name: "parallel assign", src: `package sub
func F(a, b int32) int32 {
	a, b = b, a
	return a - b
}`, want: []string{"var t2 i32 = b#1", "var t3 i32 = a#0", "a#0 = t2", "b#1 = t3"}},
		{name: "compound assign", src: `package sub
func F(a [4]int32, i int32) int32 {
	a[i&3] += 2
	a[0] <<= 1
	return a[i&3]
}`, want: []string{"a#0[(i#1 & 3)] = (a#0[(i#1 & 3)] + 2)", "a#0[0] = (a#0[0] << 1)"}},
		{name: "compound index with call", src: `package sub
func g(x int32) int32 { return x & 1 }
func F(a [4]int32, i int32) int32 {
	a[g(i)] += 2
	return a[0]
}`, want: []string{"var t2 i32 = g(i#1)", "a#0[t2] = (a#0[t2] + 2)"}},
		{name: "for cond with call", src: `package sub
func g(x int32) bool { return x < 4 }
func h(x int32) bool { return x > 0 }
func F() int32 {
	n := int32(0)
	for i := int32(0); g(i) && h(i); i++ {
		n += i
	}
	return n
}`, want: []string{"for ; ; i#1 = (i#1 + 1) {", "var t2 bool = g(i#1)", "if (! t2) {", "break"}},
		{name: "for post inlined", src: `package sub
func g(x int32) int32 { return x + 1 }
func F() int32 {
	n := int32(0)
	for i := int32(0); i < 4; i = g(i) + g(i) {
		if i == 2 {
			continue
		}
		n += i
	}
	return n
}`, want: []string{"for ; (i#1 < 4);  {", "var t2 i32 = g(i#1)", "i#1 = (t2 + g(i#1))", "continue"}},
		{name: "store as for post joins the body", src: `package sub
import "example.com/compute"
func F(w *compute.Words) {
	for i := uint32(0); i < 4; w[i] = i {
		if i == 2 {
			i++
			continue
		}
		i++
	}
}`, want: []string{"for ; (i#1 < 4);  {", "op.Store(w#0, i#1, i#1)", "continue"}},
		{name: "switch", src: `package sub
func F(x int32) int32 {
	switch y := x & 3; y {
	case 0, 1:
		return 1
	default:
		return 2
	}
}`, want: []string{"var y#1 i32 = (x#0 & 3)", "switch y#1 {", "case 0, 1:", "default:"}},
		{name: "else if with call", src: `package sub
func g(x int32) bool { return x > 0 }
func F(a, b int32) int32 {
	if a > 0 {
		return 1
	} else if b > 0 && g(b) {
		return 2
	}
	return 3
}`, want: []string{"} else {", "var t2 bool = (b#1 > 0)"}},
		{name: "composite literals", src: `package sub
type P struct {
	A uint32
	B [2]int32
}
func F() uint32 {
	p := P{B: [2]int32{1: 5}}
	q := P{7, [2]int32{1, 2}}
	return p.A + q.A
}`, want: []string{"var p#0 struct#0 = struct#0{0, [2]i32{0, 5}}", "var q#1 struct#0 = struct#0{7, [2]i32{1, 2}}"}},
		{name: "zero declaration", src: `package sub
type P struct{ A uint32 }
func F() uint32 {
	var p P
	var n int32
	return p.A + uint32(n)
}`, want: []string{"var p#0 struct#0 = zero(struct#0)", "var n#1 i32 = 0", "u32(n#1)"}},
		{name: "conversion of narrow", src: `package sub
func F(a, b uint8) uint32 {
	return uint32(a + b)
}`, want: []string{"return u32(mask.u8((a#0 + b#1)))"}},
		{name: "len folds", src: `package sub
func F(a [6]int32) int32 {
	return a[len(a)-1]
}`, want: []string{"return a#0[5]"}},
		{name: "runtime helpers", src: `package sub
import "example.com/compute"
func F(b *compute.Buf, w *compute.Words, i uint32) uint32 {
	b.PutU8(i, uint8(w[i]))
	w[i] = uint32(b.U8(i))
	return w[i] + uint32(min(int32(i), 3, 7))
}`, want: []string{"var t3 u32 = op.Load(w#1, i#2)", "op.PutU8(b#0, i#2, u8(t3))", "var t4 u8 = op.U8(b#0, i#2)", "op.Store(w#1, i#2, u32(t4))", "(op.Load(w#1, i#2) + u32(op.MinI32(op.MinI32(i32(i#2), 3), 7)))"}},
		{name: "multi define redeclares", src: `package sub
func F(a, b int32) int32 {
	x := a
	x, y := b, a
	return x + y
}`, want: []string{"var x#2 i32 = a#0", "var t3 i32 = b#1", "var t4 i32 = a#0", "x#2 = t3", "var y#5 i32 = t4", "return (x#2 + y#5)"}},
		{name: "buffer passthrough", src: `package sub
import "example.com/compute"
func g(b *compute.Buf, i uint32) uint8 { return b.U8(i) }
func Entry(b *compute.Buf, x uint32) {
	b.PutU8(x, g(b, x))
}`, want: []string{"func#0 g(b#0 Buf, i#1 u32) u8", "entry Entry coords=1 buffer0=Buf"}, entries: []string{"Entry"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dump := lower(t, c.src, c.entries...)
			for _, w := range c.want {
				if !strings.Contains(dump, w) {
					t.Errorf("dump lacks %q\n%s", w, dump)
				}
			}
		})
	}
}

// TestLowerBlankDefine lowers a parallel definition with a blank
// name: both values are evaluated into temporaries, in order, the
// named one declared from its temporary, and the blank one declares
// nothing and leaves no statement behind.
func TestLowerBlankDefine(t *testing.T) {
	dump := lower(t, `package sub
func g(x int32) int32 { return x + 1 }
func F(a int32) int32 {
	x, _ := g(a), g(a+1)
	return x
}
`)
	for _, want := range []string{"var t1 i32 = g(a#0)", "var t2 i32 = g((a#0 + 1))", "var x#3 i32 = t1", "return x#3"} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump lacks %q\n%s", want, dump)
		}
	}
	if bare := regexp.MustCompile(`(?m)^\s*t\d+\s*$`); bare.MatchString(dump) {
		t.Errorf("the blank name left a statement of its own\n%s", dump)
	}
}

func TestLowerLayout(t *testing.T) {
	dump := lower(t, `package sub
type A struct {
	X uint32
	Y int64
	Z [3]uint16
	W bool
}
type B struct {
	Inner A
	Tail  int32
}
`)
	for _, want := range []string{
		"struct#0 A size=24 align=8", "X u32 @0", "Y i64 @8", "Z [3]u16 @16", "W bool @22",
		"struct#1 B size=32 align=8", "Inner struct#0 @0", "Tail i32 @24",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump lacks %q\n%s", want, dump)
		}
	}
}
