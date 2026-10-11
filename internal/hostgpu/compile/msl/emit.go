// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package msl emits Metal Shading Language source from the compute IR.
// It is a printer: every name is synthesized from an IR index, every
// operator expression is parenthesized, and no Go is interpreted. The
// Metal runtime compiler compiles the output.
package msl

import (
	"fmt"
	"go/constant"
	"math"
	"strings"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// EntryPoint describes one emitted entry point: its function name in
// the source, the buffer bindings in order, the binding of the params
// struct (-1 when the entry takes none) and of the dispatch record,
// and the workgroup size the host dispatches with.
type EntryPoint struct {
	Name            string
	Buffers         []compile.BufferBinding
	ParamsBinding   int
	DispatchBinding int
	Workgroup       [2]int
	BufferLengths   bool // extra binding after Dispatch, word counts by buffer index
}

// Output is the emitted source and its entry points.
type Output struct {
	Source  string
	Entries []EntryPoint
}

// Emit prints the module as one MSL source file.
func Emit(m *compile.Module) (*Output, error) {
	e := &emitter{m: m}
	if err := e.module(); err != nil {
		return nil, err
	}
	return &Output{Source: e.b.String(), Entries: e.entries}, nil
}

type emitter struct {
	m       *compile.Module
	b       strings.Builder
	fn      *compile.Func
	entries []EntryPoint
	depth   int
	err     error
}

// prelude is the renderer operations. Byte operations compose
// big-endian values from masked byte loads; every operation takes the
// buffer's address mask so one definition serves every buffer size.
const prelude = `#include <metal_stdlib>
using namespace metal;

struct Dispatch { uint bx; uint by; uint off; uint stride; uint seg; };
struct WordView { device uint* data; uint length; };
struct ByteView { device uchar* data; uint length; };

static inline uint rt_u8(device const uchar* b, uint a, uint m) { return (uint)b[a & m]; }
static inline uint rt_u16be(device const uchar* b, uint a, uint m) {
	return ((uint)b[a & m] << 8) | (uint)b[(a + 1u) & m];
}
static inline uint rt_u32be(device const uchar* b, uint a, uint m) {
	return ((uint)b[a & m] << 24) | ((uint)b[(a + 1u) & m] << 16) | ((uint)b[(a + 2u) & m] << 8) | (uint)b[(a + 3u) & m];
}
static inline void rt_put_u8(device uchar* b, uint a, uint v, uint m) { b[a & m] = (uchar)v; }
static inline void rt_put_u16be(device uchar* b, uint a, uint v, uint m) {
	b[a & m] = (uchar)(v >> 8);
	b[(a + 1u) & m] = (uchar)v;
}
static inline void rt_put_u32be(device uchar* b, uint a, uint v, uint m) {
	b[a & m] = (uchar)(v >> 24);
	b[(a + 1u) & m] = (uchar)(v >> 16);
	b[(a + 2u) & m] = (uchar)(v >> 8);
	b[(a + 3u) & m] = (uchar)v;
}
static inline uint rt_load(device const uint* w, uint i, uint m) { return w[i & m]; }
static inline void rt_store(device uint* w, uint i, uint v, uint m) { w[i & m] = v; }
static inline void rt_atomic_max(device uint* w, uint i, uint v, uint m) {
	atomic_fetch_max_explicit(reinterpret_cast<device atomic_uint*>(&w[i & m]), v, memory_order_relaxed);
}
static inline uint rt_atomic_add(device uint* w, uint i, uint v, uint m) {
	return atomic_fetch_add_explicit(reinterpret_cast<device atomic_uint*>(&w[i & m]), v, memory_order_relaxed);
}
static inline uint rt_atomic_xchg(device uint* w, uint i, uint v, uint m) {
	return atomic_exchange_explicit(reinterpret_cast<device atomic_uint*>(&w[i & m]), v, memory_order_relaxed);
}
static inline uint rt_view_load(WordView w, uint i) { return rt_load(w.data, i, w.length - 1u); }
static inline void rt_view_store(WordView w, uint i, uint v) { rt_store(w.data, i, v, w.length - 1u); }
static inline void rt_view_atomic_max(WordView w, uint i, uint v) { rt_atomic_max(w.data, i, v, w.length - 1u); }
static inline uint rt_view_atomic_add(WordView w, uint i, uint v) { return rt_atomic_add(w.data, i, v, w.length - 1u); }
static inline uint rt_view_atomic_xchg(WordView w, uint i, uint v) { return rt_atomic_xchg(w.data, i, v, w.length - 1u); }
static inline uint rt_view_u8(ByteView b, uint a) { return rt_u8(b.data, a, b.length - 1u); }
static inline uint rt_view_u16be(ByteView b, uint a) { return rt_u16be(b.data, a, b.length - 1u); }
static inline uint rt_view_u32be(ByteView b, uint a) { return rt_u32be(b.data, a, b.length - 1u); }
static inline void rt_view_put_u8(ByteView b, uint a, uint v) { rt_put_u8(b.data, a, v, b.length - 1u); }
static inline void rt_view_put_u16be(ByteView b, uint a, uint v) { rt_put_u16be(b.data, a, v, b.length - 1u); }
static inline void rt_view_put_u32be(ByteView b, uint a, uint v) { rt_put_u32be(b.data, a, v, b.length - 1u); }
`

func (e *emitter) module() error {
	e.b.WriteString(prelude)
	e.b.WriteByte('\n')
	e.structs()
	for i, t := range e.m.Tables {
		fmt.Fprintf(&e.b, "constant %s tb%d = {", e.typ(t.Type), i)
		for j, v := range t.Values {
			if j > 0 {
				e.b.WriteString(", ")
			}
			e.b.WriteString(e.constant(t.Type.Elem, v))
		}
		fmt.Fprintf(&e.b, "}; // %s\n", t.Name)
	}
	e.b.WriteByte('\n')
	for i := range e.m.Funcs {
		e.prototype(i)
		e.b.WriteString(";\n")
	}
	e.b.WriteByte('\n')
	for i := range e.m.Funcs {
		e.function(i)
	}
	for i, en := range e.m.Entries {
		e.entryShell(i, en)
	}
	return e.err
}

// structs prints every struct after the structs its fields use.
func (e *emitter) structs() {
	done := make([]bool, len(e.m.Structs))
	var visit func(i int)
	visit = func(i int) {
		if done[i] {
			return
		}
		done[i] = true
		st := e.m.Structs[i]
		for _, f := range st.Fields {
			for _, dep := range structDeps(f.Type) {
				visit(dep)
			}
		}
		fmt.Fprintf(&e.b, "struct S%d { // %s\n", i, st.Name)
		for j, f := range st.Fields {
			fmt.Fprintf(&e.b, "\t%s f%d; // %s\n", e.typ(f.Type), j, f.Name)
		}
		e.b.WriteString("};\n")
	}
	for i := range e.m.Structs {
		visit(i)
	}
	e.b.WriteByte('\n')
}

// structDeps lists the struct indices a type refers to.
func structDeps(t compile.Type) []int {
	switch u := t.(type) {
	case compile.StructRef:
		return []int{u.Index}
	case compile.ArrayType:
		return structDeps(u.Elem)
	}
	return nil
}

// typ prints a type. Narrow integers are held in 32-bit registers.
func (e *emitter) typ(t compile.Type) string {
	switch u := t.(type) {
	case compile.Scalar:
		switch compile.Kind(u) {
		case compile.Bool:
			return "bool"
		case compile.I8, compile.I16, compile.I32:
			return "int"
		case compile.U8, compile.U16, compile.U32:
			return "uint"
		case compile.I64:
			return "long"
		case compile.U64:
			return "ulong"
		case compile.F32:
			return "float"
		}
	case compile.ArrayType:
		return fmt.Sprintf("array<%s, %d>", e.typ(u.Elem), u.Len)
	case compile.StructRef:
		return fmt.Sprintf("S%d", u.Index)
	case compile.BufferType:
		if u.Dynamic {
			if u.Word {
				return "WordView"
			}
			return "ByteView"
		}
		elem := "uchar"
		if u.Word {
			elem = "uint"
		}
		if u.Params {
			return "device const " + elem + "*"
		}
		return "device " + elem + "*"
	}
	e.fail("unsupported type %s", t)
	return "?"
}

func (e *emitter) fail(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf(format, args...)
	}
}

// prototype prints a function signature: value and ref parameters,
// buffers, then one out reference per result when there are several.
func (e *emitter) prototype(i int) {
	fn := &e.m.Funcs[i]
	ret := "void"
	if len(fn.Results) == 1 {
		ret = e.typ(fn.Results[0])
	}
	fmt.Fprintf(&e.b, "static %s fn%d(", ret, i)
	for p := 0; p < fn.NumParams; p++ {
		if p > 0 {
			e.b.WriteString(", ")
		}
		l := fn.Locals[p]
		switch l.Kind {
		case compile.RefParam:
			fmt.Fprintf(&e.b, "thread %s& l%d", e.typ(l.Type), p)
		default:
			fmt.Fprintf(&e.b, "%s l%d", e.typ(l.Type), p)
		}
	}
	if len(fn.Results) > 1 {
		for r, t := range fn.Results {
			if fn.NumParams > 0 || r > 0 {
				e.b.WriteString(", ")
			}
			fmt.Fprintf(&e.b, "thread %s& r%d", e.typ(t), r)
		}
	}
	e.b.WriteString(")")
}

func (e *emitter) function(i int) {
	fn := &e.m.Funcs[i]
	e.fn = fn
	e.prototype(i)
	fmt.Fprintf(&e.b, " { // %s\n", fn.Name)
	e.depth = 1
	e.stmts(fn.Body.Stmts)
	e.b.WriteString("}\n\n")
	e.fn = nil
}

// GroupWorkgroup is a group entry's threadgroup width, the unit the
// Metal backend widens a group row in (GroupThreads).
const GroupWorkgroup = 128

// workgroup is the threadgroup size the host dispatches an entry
// with, chosen from the entry's form: 8x8 for a span entry,
// GroupWorkgroup x 1 for a group entry, and 64x1 for an index entry.
func workgroup(en compile.Entry) [2]int {
	switch {
	case en.Span:
		return [2]int{8, 8}
	case en.Group:
		return [2]int{GroupWorkgroup, 1}
	}
	return [2]int{64, 1}
}

// entryShell prints an entry point's shell, the MSL kernel function:
// buffers by binding, the params buffer at the next binding, the
// dispatch record after it, the thread ids, and a call into the IR
// wrapper, which holds the logic.
func (e *emitter) entryShell(i int, en compile.Entry) {
	fn := &e.m.Funcs[en.Func]
	k := EntryPoint{Name: fmt.Sprintf("k%d", i), Buffers: en.Buffers, Workgroup: workgroup(en)}
	k.ParamsBinding = -1
	k.DispatchBinding = len(en.Buffers)
	if len(en.Params) > 0 {
		k.ParamsBinding = len(en.Buffers)
		k.DispatchBinding = k.ParamsBinding + 1
	}
	fmt.Fprintf(&e.b, "kernel void k%d( // %s\n", i, fn.Name)
	var args []string
	for _, bb := range en.Buffers {
		if bb.Type.Dynamic {
			// The length the host supplies is the binding's word
			// count; a byte view holds the bytes.
			k.BufferLengths = true
			if bb.Type.Word {
				fmt.Fprintf(&e.b, "\tdevice uint* b%d [[buffer(%d)]],\n", bb.Binding, bb.Binding)
				args = append(args, fmt.Sprintf("WordView{b%d, lengths[%d]}", bb.Binding, bb.Binding))
			} else {
				fmt.Fprintf(&e.b, "\tdevice uchar* b%d [[buffer(%d)]],\n", bb.Binding, bb.Binding)
				args = append(args, fmt.Sprintf("ByteView{b%d, lengths[%d] * 4u}", bb.Binding, bb.Binding))
			}
		} else {
			fmt.Fprintf(&e.b, "\t%s b%d [[buffer(%d)]],\n", e.typ(bb.Type), bb.Binding, bb.Binding)
			args = append(args, fmt.Sprintf("b%d", bb.Binding))
		}
	}
	if k.ParamsBinding >= 0 {
		fmt.Fprintf(&e.b, "\tdevice const uchar* pp [[buffer(%d)]],\n", k.ParamsBinding)
		args = append(args, "pp")
	}
	fmt.Fprintf(&e.b, "\tconstant Dispatch& d [[buffer(%d)]],\n", k.DispatchBinding)
	if k.BufferLengths {
		fmt.Fprintf(&e.b, "\tconstant uint* lengths [[buffer(%d)]],\n", k.DispatchBinding+1)
	}
	args = append(args, "d.bx", "d.by", "d.off", "d.stride", "d.seg")
	if en.Group {
		// A group entry's group is a row of the grid, its threads the
		// threadgroups dispatched across the row: the wrapper takes
		// the thread's index across the row as gx, the row as gy, and
		// the row's thread count, the threadgroup size by the
		// threadgroups across, as the extra word. Dispatched one
		// threadgroup across, the group is the one threadgroup the
		// barrier orders.
		e.b.WriteString("\tuint2 lid [[thread_position_in_threadgroup]],\n")
		e.b.WriteString("\tuint2 tpg [[threads_per_threadgroup]],\n")
		e.b.WriteString("\tuint2 tgid [[threadgroup_position_in_grid]],\n")
		e.b.WriteString("\tuint2 tgpg [[threadgroups_per_grid]])\n{\n")
		args = append(args, "(tgid.x * tpg.x + lid.x)", "tgid.y", "(tpg.x * tgpg.x)")
	} else {
		// Every other entry takes both grid coordinates: an index
		// entry's row selects its params record, and a
		// one-dimensional dispatch leaves y at zero. Metal issues a
		// grid of any width in one dispatch, so the grid id is the
		// thread's own.
		e.b.WriteString("\tuint2 gid [[thread_position_in_grid]])\n{\n")
		args = append(args, "gid.x", "gid.y", "0u")
	}
	fmt.Fprintf(&e.b, "\tfn%d(%s);\n}\n\n", en.Wrapper, strings.Join(args, ", "))
	e.entries = append(e.entries, k)
}

func (e *emitter) line(format string, args ...any) {
	e.b.WriteString(strings.Repeat("\t", e.depth))
	fmt.Fprintf(&e.b, format, args...)
	e.b.WriteByte('\n')
}

func (e *emitter) stmts(list []compile.Stmt) {
	for _, s := range list {
		e.stmt(s)
	}
}

func (e *emitter) block(b *compile.Block) {
	e.depth++
	e.stmts(b.Stmts)
	e.depth--
}

func (e *emitter) stmt(s compile.Stmt) {
	switch s := s.(type) {
	case *compile.Block:
		e.line("{")
		e.block(s)
		e.line("}")
	case *compile.VarDecl:
		l := e.fn.Locals[s.Local]
		e.line("%s l%d = %s;", e.typ(l.Type), s.Local, e.expr(s.Init))
	case *compile.Assign:
		if len(s.Targets) > 1 {
			e.line("%s;", e.callWithOut(s.Values[0].(*compile.Call), s.Targets))
			return
		}
		e.line("%s = %s;", e.expr(s.Targets[0]), e.expr(s.Values[0]))
	case *compile.ExprStmt:
		if r, ok := s.X.(*compile.OpCall); ok && !r.Op.Yields {
			// An operation with no result prints its own line.
			e.operation(r)
			return
		}
		e.line("%s;", e.expr(s.X))
	case *compile.If:
		e.ifChain(s, "if")
	case *compile.Switch:
		e.line("switch %s {", wrapped(e.expr(s.Tag)))
		for _, c := range s.Cases {
			for _, v := range c.Values {
				e.line("case %s:", e.expr(v))
			}
			e.line("{")
			e.block(c.Body)
			e.line("} break;")
		}
		if s.Default != nil {
			e.line("default:")
			e.line("{")
			e.block(s.Default)
			e.line("} break;")
		}
		e.line("}")
	case *compile.For:
		cond, post := "", ""
		if s.Cond != nil {
			cond = e.expr(s.Cond)
		}
		if s.Post != nil {
			post = e.simple(s.Post)
		}
		e.line("for (; %s; %s) {", cond, post)
		e.block(s.Body)
		e.line("}")
	case *compile.Break:
		e.line("break;")
	case *compile.Continue:
		e.line("continue;")
	case *compile.Return:
		switch len(s.Values) {
		case 0:
			e.line("return;")
		case 1:
			e.line("return %s;", e.expr(s.Values[0]))
		default:
			// Results evaluate before any out parameter is written.
			for i, v := range s.Values {
				e.line("%s rv%d = %s;", e.typ(e.fn.Results[i]), i, e.expr(v))
			}
			for i := range s.Values {
				e.line("r%d = rv%d;", i, i)
			}
			e.line("return;")
		}
	default:
		e.fail("unsupported statement %T", s)
	}
}

// ifChain prints an if with else-if continuation.
func (e *emitter) ifChain(s *compile.If, kw string) {
	e.line("%s %s {", kw, wrapped(e.expr(s.Cond)))
	e.block(s.Then)
	switch el := s.Else.(type) {
	case nil:
		e.line("}")
	case *compile.Block:
		e.line("} else {")
		e.block(el)
		e.line("}")
	case *compile.If:
		e.b.WriteString(strings.Repeat("\t", e.depth))
		e.b.WriteString("} else ")
		saved := e.depth
		e.depth = 0
		e.ifChain(el, "if")
		e.depth = saved
	}
}

// wrapped returns the expression in one pair of parentheses: its own
// when it already is a single parenthesized group, else a new pair.
func wrapped(x string) string {
	if len(x) >= 2 && x[0] == '(' && x[len(x)-1] == ')' {
		depth := 0
		for i := 0; i < len(x)-1; i++ {
			switch x[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				return "(" + x + ")"
			}
		}
		return x
	}
	return "(" + x + ")"
}

// simple prints a loop post statement without indentation or the
// terminating semicolon.
func (e *emitter) simple(s compile.Stmt) string {
	switch s := s.(type) {
	case *compile.Assign:
		if len(s.Targets) == 1 {
			return e.expr(s.Targets[0]) + " = " + e.expr(s.Values[0])
		}
		return e.callWithOut(s.Values[0].(*compile.Call), s.Targets)
	case *compile.ExprStmt:
		return e.expr(s.X)
	}
	e.fail("unsupported loop post statement %T", s)
	return ""
}

// callWithOut prints a multi-result call with its targets as the out
// arguments.
func (e *emitter) callWithOut(c *compile.Call, targets []compile.Expr) string {
	args := e.args(c.Args)
	for _, t := range targets {
		args = append(args, e.expr(t))
	}
	return fmt.Sprintf("fn%d(%s)", c.Func, strings.Join(args, ", "))
}

func (e *emitter) args(list []compile.Expr) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = e.expr(a)
	}
	return out
}

func (e *emitter) expr(x compile.Expr) string {
	switch x := x.(type) {
	case *compile.Const:
		return e.constant(x.T, x.Value)
	case *compile.LocalRef:
		return fmt.Sprintf("l%d", x.Index)
	case *compile.FieldRef:
		return fmt.Sprintf("%s.f%d", e.expr(x.X), x.Field)
	case *compile.IndexRef:
		return fmt.Sprintf("%s[%s]", e.expr(x.X), e.expr(x.Index))
	case *compile.TableRef:
		return fmt.Sprintf("tb%d[%s]", x.Table, e.expr(x.Index))
	case *compile.Zero:
		return e.typ(x.T) + "{}"
	case *compile.Composite:
		return e.typ(x.T) + "{" + strings.Join(e.args(x.Elems), ", ") + "}"
	case *compile.Call:
		return fmt.Sprintf("fn%d(%s)", x.Func, strings.Join(e.args(x.Args), ", "))
	case *compile.OpCall:
		return e.operation(x)
	case *compile.Unary:
		if x.Op == compile.Neg {
			if s, u, ok := wrapTypes(x.T); ok {
				return fmt.Sprintf("((%s)(-(%s)%s))", s, u, e.expr(x.X))
			}
		}
		op := map[compile.Op]string{compile.Neg: "-", compile.Not: "!", compile.BitComplement: "~"}[x.Op]
		return fmt.Sprintf("(%s%s)", op, e.expr(x.X))
	case *compile.Binary:
		switch x.Op {
		case compile.AndNot:
			return fmt.Sprintf("(%s & ~%s)", e.expr(x.X), e.expr(x.Y))
		case compile.Add, compile.Sub, compile.Mul:
			if s, u, ok := wrapTypes(x.T); ok {
				return fmt.Sprintf("((%s)((%s)%s %s (%s)%s))", s, u, e.expr(x.X), x.Op, u, e.expr(x.Y))
			}
		case compile.Shl:
			if s, u, ok := wrapTypes(x.T); ok {
				return fmt.Sprintf("((%s)((%s)%s << %s))", s, u, e.expr(x.X), e.expr(x.Y))
			}
		}
		return fmt.Sprintf("(%s %s %s)", e.expr(x.X), x.Op, e.expr(x.Y))
	case *compile.Convert:
		return e.convert(x.T.(compile.Scalar), e.expr(x.X))
	case *compile.Mask:
		return e.mask(x.T.(compile.Scalar), e.expr(x.X))
	case *compile.LoadStruct:
		return fmt.Sprintf("(*(device const %s*)(%s + %s))", e.typ(x.T), e.expr(x.Buffer), e.expr(x.Addr))
	}
	e.fail("unsupported expression %T", x)
	return "?"
}

// wrapTypes is the signed C type of a signed scalar and the unsigned
// type of its register width, through which its add, subtract,
// multiply, negate, and shift left are printed: MSL's C++ base
// leaves signed overflow undefined while Go wraps, and unsigned
// arithmetic wraps by definition, with the conversion back two's
// complement on every Metal target. Narrow types share int's
// register, so their operations run on uint before the Mask.
func wrapTypes(t compile.Type) (signed, unsigned string, ok bool) {
	s, isScalar := t.(compile.Scalar)
	if !isScalar {
		return "", "", false
	}
	switch compile.Kind(s) {
	case compile.I8, compile.I16, compile.I32:
		return "int", "uint", true
	case compile.I64:
		return "long", "ulong", true
	}
	return "", "", false
}

// mask re-canonicalizes a narrow value: sign-extension through the
// narrow C type for signed, an AND for unsigned.
func (e *emitter) mask(t compile.Scalar, x string) string {
	switch compile.Kind(t) {
	case compile.I8:
		return "((int)(char)" + x + ")"
	case compile.I16:
		return "((int)(short)" + x + ")"
	case compile.U8:
		return "(" + x + " & 0xffu)"
	case compile.U16:
		return "(" + x + " & 0xffffu)"
	}
	return x
}

// convert prints a conversion; a narrow target canonicalizes.
func (e *emitter) convert(t compile.Scalar, x string) string {
	switch compile.Kind(t) {
	case compile.I8, compile.I16:
		return e.mask(t, x)
	case compile.U8, compile.U16:
		return e.mask(t, "(uint)"+x)
	}
	return "((" + e.typ(t) + ")" + x + ")"
}

// operation evaluates an OpCall node's operands and calls its
// operation on this emitter, which implements compute.Operations over
// a buffer's local and source text. An operation with a result
// returns its expression; one without prints its own line.
func (e *emitter) operation(x *compile.OpCall) string {
	var buf *compile.LocalRef
	args := x.Args
	if x.Op.Buffered {
		buf = args[0].(*compile.LocalRef)
		args = args[1:]
	}
	return compile.Invoke(e, x.Op, buf, e.args(args))
}

// access prints a buffer operation's prelude call: the fixed form
// named name on a fixed buffer, with the buffer's size mask appended,
// or the view form, rt_view_ in place of rt_, on a variable-length
// buffer, which masks with the bound count the binding supplied,
// minus one. A variable-length buffer must be a word buffer for a
// word operation and a byte buffer otherwise.
func (e *emitter) access(buf *compile.LocalRef, name string, word bool, args ...string) string {
	bt := buf.T.(compile.BufferType)
	args = append([]string{e.expr(buf)}, args...)
	if bt.Dynamic {
		if bt.Word != word {
			kind := "byte"
			if bt.Word {
				kind = "word"
			}
			e.fail("%s on a variable %s buffer", name, kind)
			return "?"
		}
		return "rt_view_" + strings.TrimPrefix(name, "rt_") + "(" + strings.Join(args, ", ") + ")"
	}
	args = append(args, fmt.Sprintf("%du", bt.Size-1))
	return name + "(" + strings.Join(args, ", ") + ")"
}

// The renderer operations: buffer operations through the prelude,
// integer operations through the Metal library, and the phase
// boundary as the threadgroup barrier over device memory.

func (e *emitter) U8(buf *compile.LocalRef, a string) string { return e.access(buf, "rt_u8", false, a) }

func (e *emitter) U16BE(buf *compile.LocalRef, a string) string {
	return e.access(buf, "rt_u16be", false, a)
}

func (e *emitter) U32BE(buf *compile.LocalRef, a string) string {
	return e.access(buf, "rt_u32be", false, a)
}

func (e *emitter) PutU8(buf *compile.LocalRef, a, v string) {
	e.line("%s;", e.access(buf, "rt_put_u8", false, a, v))
}

func (e *emitter) PutU16BE(buf *compile.LocalRef, a, v string) {
	e.line("%s;", e.access(buf, "rt_put_u16be", false, a, v))
}

func (e *emitter) PutU32BE(buf *compile.LocalRef, a, v string) {
	e.line("%s;", e.access(buf, "rt_put_u32be", false, a, v))
}

func (e *emitter) Load(buf *compile.LocalRef, i string) string {
	return e.access(buf, "rt_load", true, i)
}

func (e *emitter) Store(buf *compile.LocalRef, i, v string) {
	e.line("%s;", e.access(buf, "rt_store", true, i, v))
}

func (e *emitter) Length(buf *compile.LocalRef) string { return e.expr(buf) + ".length" }

func (e *emitter) Raise(buf *compile.LocalRef, i, v string) {
	e.line("%s;", e.access(buf, "rt_atomic_max", true, i, v))
}

func (e *emitter) Add(buf *compile.LocalRef, i, v string) string {
	return e.access(buf, "rt_atomic_add", true, i, v)
}

func (e *emitter) Swap(buf *compile.LocalRef, i, v string) string {
	return e.access(buf, "rt_atomic_xchg", true, i, v)
}

func (e *emitter) MinI32(x, y string) string { return "min(" + x + ", " + y + ")" }

func (e *emitter) MaxI32(x, y string) string { return "max(" + x + ", " + y + ")" }

func (e *emitter) MinI64(x, y string) string { return "min(" + x + ", " + y + ")" }

func (e *emitter) MaxI64(x, y string) string { return "max(" + x + ", " + y + ")" }

func (e *emitter) MinU32(x, y string) string { return "min(" + x + ", " + y + ")" }

func (e *emitter) MaxU32(x, y string) string { return "max(" + x + ", " + y + ")" }

func (e *emitter) NextPhase() { e.line("threadgroup_barrier(mem_flags::mem_device);") }

// constant prints a typed constant with the suffix its type needs.
func (e *emitter) constant(t compile.Type, v constant.Value) string {
	s, ok := t.(compile.Scalar)
	if !ok {
		e.fail("constant of non-scalar type %s", t)
		return "?"
	}
	switch compile.Kind(s) {
	case compile.Bool:
		if constant.BoolVal(v) {
			return "true"
		}
		return "false"
	case compile.F32:
		f, _ := constant.Float64Val(v)
		return fmt.Sprintf("%sf", formatFloat(float32(f)))
	case compile.I64:
		n, _ := constant.Int64Val(constant.ToInt(v))
		if n == math.MinInt64 {
			return "(-9223372036854775807L - 1)"
		}
		if n < 0 {
			return fmt.Sprintf("(%dL)", n)
		}
		return fmt.Sprintf("%dL", n)
	case compile.U64:
		n, _ := constant.Uint64Val(constant.ToInt(v))
		return fmt.Sprintf("%dUL", n)
	case compile.U8, compile.U16, compile.U32:
		n, _ := constant.Uint64Val(constant.ToInt(v))
		return fmt.Sprintf("%du", n)
	default:
		n, _ := constant.Int64Val(constant.ToInt(v))
		if n == math.MinInt32 {
			return "(-2147483647 - 1)"
		}
		if n < 0 {
			return fmt.Sprintf("(%d)", n)
		}
		return fmt.Sprintf("%d", n)
	}
}

// formatFloat prints a float32 exactly enough to round-trip.
func formatFloat(f float32) string {
	s := fmt.Sprintf("%.9g", f)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
