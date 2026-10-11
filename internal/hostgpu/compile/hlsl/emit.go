// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hlsl

import (
	"fmt"
	"go/constant"
	"math"
	"strings"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// EntryPoint describes one emitted entry point: the entry's index,
// the entry's Go function name, which names its compiled blob, the
// source holding every function and this entry's shell, the root
// signature layout the shell declares, which carries the workgroup
// the host dispatches with. The shell's kernel function is named main
// in every source, so one compiler command serves every file.
type EntryPoint struct {
	Entry  int
	Func   string
	Source string
	Layout Layout
}

// Output is one source per entry point.
type Output struct {
	Entries []EntryPoint
}

// Emit prints one HLSL source per entry point: the prelude, the
// structs and their params loaders, the tables, every function, and
// the entry's shell.
func Emit(m *compile.Module) (*Output, error) {
	e := &emitter{m: m, lengths: Lengths(m)}
	if err := e.common(); err != nil {
		return nil, err
	}
	out := &Output{}
	for i, en := range m.Entries {
		k, err := e.entryShell(i, en)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, k)
	}
	return out, nil
}

type emitter struct {
	m       *compile.Module
	b       strings.Builder
	fn      *compile.Func
	lengths int
	depth   int
	hoisted map[compile.Expr]string
	temps   int
	err     error

	refWrites map[[2]int]bool // writesRef, by function and parameter
	rows      map[[2]int]bool // rowLocals, by function and local
	fnIndex   int             // the function being printed
}

// prelude is the renderer operations over the two buffer forms. A byte
// buffer is a raw buffer of words: a byte read is a word load and a
// lane extract, and a byte write is a lane-masked atomic AND then OR
// on the containing word, so threads writing distinct bytes of one
// word keep each other's bytes. Every address is masked with the
// buffer's size mask before the word address is formed. A halfword
// read or write is two byte operations: the one-word form, the bytes
// swapped by shifts and masks, is miscompiled by NVIDIA's DXIL
// compiler in the VDP1 draw kernels. A quad on a word boundary takes
// one load or one store, and one that is not is read as four bytes
// and written as two halfwords. The aligned quad store writes every
// lane of its word, which is safe beside the atomics because two
// threads writing one word in one dispatch would write a byte in
// common, the collision the compute functions exclude.
const prelude = `uint rt_u8(RWByteAddressBuffer b, uint a, uint m) {
	a &= m;
	return (b.Load(a & ~3u) >> ((a & 3u) * 8u)) & 0xffu;
}
uint rt_u16be(RWByteAddressBuffer b, uint a, uint m) {
	return (rt_u8(b, a, m) << 8) | rt_u8(b, a + 1u, m);
}
uint rt_u32be(RWByteAddressBuffer b, uint a, uint m) {
	uint a0 = a & m;
	if ((a0 & 3u) == 0u) {
		uint w = b.Load(a0);
		return (w << 24) | ((w & 0xff00u) << 8) | ((w >> 8) & 0xff00u) | (w >> 24);
	}
	return (rt_u8(b, a, m) << 24) | (rt_u8(b, a + 1u, m) << 16) | (rt_u8(b, a + 2u, m) << 8) | rt_u8(b, a + 3u, m);
}
void rt_put_u8(RWByteAddressBuffer b, uint a, uint v, uint m) {
	a &= m;
	uint s = (a & 3u) * 8u;
	uint w = a & ~3u;
	b.InterlockedAnd(w, ~(0xffu << s));
	b.InterlockedOr(w, (v & 0xffu) << s);
}
void rt_put_u16be(RWByteAddressBuffer b, uint a, uint v, uint m) {
	rt_put_u8(b, a, v >> 8, m);
	rt_put_u8(b, a + 1u, v, m);
}
void rt_put_u32be(RWByteAddressBuffer b, uint a, uint v, uint m) {
	uint a0 = a & m;
	if ((a0 & 3u) == 0u) {
		b.Store(a0, (v << 24) | ((v & 0xff00u) << 8) | ((v >> 8) & 0xff00u) | (v >> 24));
		return;
	}
	rt_put_u16be(b, a, v >> 16, m);
	rt_put_u16be(b, a + 2u, v, m);
}
uint64_t rt_load64(RWByteAddressBuffer b, uint a) {
	uint2 v = b.Load2(a);
	return ((uint64_t)v.y << 32) | (uint64_t)v.x;
}
uint rt_load(RWStructuredBuffer<uint> w, uint i, uint m) { return w[i & m]; }
void rt_store(RWStructuredBuffer<uint> w, uint i, uint v, uint m) { w[i & m] = v; }
void rt_atomic_max(RWStructuredBuffer<uint> w, uint i, uint v, uint m) { InterlockedMax(w[i & m], v); }
uint rt_atomic_add(RWStructuredBuffer<uint> w, uint i, uint v, uint m) { uint p; InterlockedAdd(w[i & m], v, p); return p; }
uint rt_atomic_xchg(RWStructuredBuffer<uint> w, uint i, uint v, uint m) { uint p; InterlockedExchange(w[i & m], v, p); return p; }
uint rt_view_load(RWStructuredBuffer<uint> w, uint n, uint i) { return rt_load(w, i, n - 1u); }
void rt_view_store(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { rt_store(w, i, v, n - 1u); }
void rt_view_atomic_max(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { rt_atomic_max(w, i, v, n - 1u); }
uint rt_view_atomic_add(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { return rt_atomic_add(w, i, v, n - 1u); }
uint rt_view_atomic_xchg(RWStructuredBuffer<uint> w, uint n, uint i, uint v) { return rt_atomic_xchg(w, i, v, n - 1u); }
uint rt_view_u8(RWByteAddressBuffer b, uint n, uint a) { return rt_u8(b, a, n - 1u); }
uint rt_view_u16be(RWByteAddressBuffer b, uint n, uint a) { return rt_u16be(b, a, n - 1u); }
uint rt_view_u32be(RWByteAddressBuffer b, uint n, uint a) { return rt_u32be(b, a, n - 1u); }
void rt_view_put_u8(RWByteAddressBuffer b, uint n, uint a, uint v) { rt_put_u8(b, a, v, n - 1u); }
void rt_view_put_u16be(RWByteAddressBuffer b, uint n, uint a, uint v) { rt_put_u16be(b, a, v, n - 1u); }
void rt_view_put_u32be(RWByteAddressBuffer b, uint n, uint a, uint v) { rt_put_u32be(b, a, v, n - 1u); }
int64_t rt_min_i64(int64_t a, int64_t b) { if (a < b) { return a; } return b; }
int64_t rt_max_i64(int64_t a, int64_t b) { if (a > b) { return a; } return b; }
`

// common prints everything the entries share: the prelude, the structs
// and the params loaders, the tables, the prototypes, and the functions.
func (e *emitter) common() error {
	e.b.WriteString(prelude)
	e.b.WriteString("static uint rt_prow; // the params row's address, which the wrapper sets\n")
	e.b.WriteByte('\n')
	e.structs()
	e.loaders()
	for i, t := range e.m.Tables {
		fmt.Fprintf(&e.b, "static const %s = {", e.decl(t.Type, fmt.Sprintf("tb%d", i)))
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
			fmt.Fprintf(&e.b, "\t%s; // %s\n", e.decl(f.Type, fmt.Sprintf("f%d", j)), f.Name)
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

// loaders prints a params loader per struct an entry's wrapper loads,
// the entry's first struct parameter, and per struct nested in one:
// a gather of the fields at their laid-out offsets from a byte
// buffer, since a raw buffer loads words. The loaders are printed in
// the structs' dependency order, so a nested struct's loader
// precedes the one that calls it.
func (e *emitter) loaders() {
	need := make([]bool, len(e.m.Structs))
	var mark func(t compile.Type)
	mark = func(t compile.Type) {
		switch u := t.(type) {
		case compile.StructRef:
			if need[u.Index] {
				return
			}
			need[u.Index] = true
			for _, f := range e.m.Structs[u.Index].Fields {
				mark(f.Type)
			}
		case compile.ArrayType:
			mark(u.Elem)
		}
	}
	for _, en := range e.m.Entries {
		if len(en.Params) > 0 {
			mark(e.m.Funcs[en.Func].Locals[en.Params[0]].Type)
		}
	}
	done := make([]bool, len(e.m.Structs))
	var visit func(i int)
	visit = func(i int) {
		if done[i] || !need[i] {
			return
		}
		done[i] = true
		st := e.m.Structs[i]
		for _, f := range st.Fields {
			for _, dep := range structDeps(f.Type) {
				visit(dep)
			}
		}
		fmt.Fprintf(&e.b, "S%d ld%d(RWByteAddressBuffer b, uint a) { // %s\n\tS%d r;\n", i, i, st.Name, i)
		for j, f := range st.Fields {
			e.loadField(f.Type, fmt.Sprintf("r.f%d", j), f.Offset)
		}
		e.b.WriteString("\treturn r;\n}\n")
	}
	for i := range e.m.Structs {
		visit(i)
	}
	e.b.WriteByte('\n')
}

// loadField prints the loads of one field of a params loader into
// dst, at the field's offset from the row: a word per 32-bit scalar
// reinterpreted by kind, two words per 64-bit scalar, the byte of a
// bool tested against zero, an element at a time for an array, and
// the nested loader for a struct.
func (e *emitter) loadField(t compile.Type, dst string, off int) {
	switch u := t.(type) {
	case compile.Scalar:
		switch compile.Kind(u) {
		case compile.Bool:
			fmt.Fprintf(&e.b, "\t%s = rt_u8(b, a + %du, 0xffffffffu) != 0u;\n", dst, off)
		case compile.I32:
			fmt.Fprintf(&e.b, "\t%s = asint(b.Load(a + %du));\n", dst, off)
		case compile.U32:
			fmt.Fprintf(&e.b, "\t%s = b.Load(a + %du);\n", dst, off)
		case compile.F32:
			fmt.Fprintf(&e.b, "\t%s = asfloat(b.Load(a + %du));\n", dst, off)
		case compile.I64:
			fmt.Fprintf(&e.b, "\t%s = (int64_t)rt_load64(b, a + %du);\n", dst, off)
		case compile.U64:
			fmt.Fprintf(&e.b, "\t%s = rt_load64(b, a + %du);\n", dst, off)
		default:
			e.fail("params field of type %s has no buffer form", t)
		}
	case compile.ArrayType:
		stride := e.size(u.Elem)
		for i := 0; i < u.Len; i++ {
			e.loadField(u.Elem, fmt.Sprintf("%s[%d]", dst, i), off+i*stride)
		}
	case compile.StructRef:
		fmt.Fprintf(&e.b, "\t%s = ld%d(b, a + %du);\n", dst, u.Index, off)
	default:
		e.fail("params field of type %s has no buffer form", t)
	}
}

// size is a buffer-visible type's size in bytes by the layout rule:
// a scalar its width, an array its element size by its length, a
// struct its laid-out size.
func (e *emitter) size(t compile.Type) int {
	switch u := t.(type) {
	case compile.Scalar:
		return u.Bytes()
	case compile.ArrayType:
		return u.Len * e.size(u.Elem)
	case compile.StructRef:
		return e.m.Structs[u.Index].Size
	}
	e.fail("size of type %s", t)
	return 0
}

// typ prints a scalar, struct, or buffer type. Narrow integers are
// held in 32-bit registers. An array is a declarator, printed by decl.
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
			return "int64_t"
		case compile.U64:
			return "uint64_t"
		case compile.F32:
			return "float"
		}
	case compile.StructRef:
		return fmt.Sprintf("S%d", u.Index)
	case compile.BufferType:
		if u.Word {
			return "RWStructuredBuffer<uint>"
		}
		return "RWByteAddressBuffer"
	}
	e.fail("unsupported type %s", t)
	return "?"
}

// decl prints a declaration of name with the type: the element type,
// the name, and one dimension per array level, outermost first.
func (e *emitter) decl(t compile.Type, name string) string {
	dims := ""
	for {
		a, ok := t.(compile.ArrayType)
		if !ok {
			break
		}
		dims += fmt.Sprintf("[%d]", a.Len)
		t = a.Elem
	}
	return e.typ(t) + " " + name + dims
}

func (e *emitter) fail(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf(format, args...)
	}
}

// outResults reports a function whose results are out parameters:
// several results, or one array, which a function cannot return.
func outResults(fn *compile.Func) bool {
	if len(fn.Results) > 1 {
		return true
	}
	if len(fn.Results) == 1 {
		_, isArray := fn.Results[0].(compile.ArrayType)
		return isArray
	}
	return false
}

// prototype prints a function signature: value, ref, and buffer
// parameters, a hidden word count after each variable-length buffer,
// then one out parameter per result when the results are out.
func (e *emitter) prototype(i int) {
	fn := &e.m.Funcs[i]
	ret := "void"
	if len(fn.Results) == 1 && !outResults(fn) {
		ret = e.typ(fn.Results[0])
	}
	fmt.Fprintf(&e.b, "%s fn%d(", ret, i)
	n := 0
	param := func(s string) {
		if n > 0 {
			e.b.WriteString(", ")
		}
		e.b.WriteString(s)
		n++
	}
	for p := 0; p < fn.NumParams; p++ {
		l := fn.Locals[p]
		name := fmt.Sprintf("l%d", p)
		switch l.Kind {
		case compile.RefParam:
			param("inout " + e.decl(l.Type, name))
		case compile.BufferParam:
			param(e.typ(l.Type) + " " + name)
			if l.Type.(compile.BufferType).Dynamic {
				param("uint " + name + "_n")
			}
		default:
			param(e.decl(l.Type, name))
		}
	}
	if outResults(fn) {
		for r, t := range fn.Results {
			param("out " + e.decl(t, fmt.Sprintf("r%d", r)))
		}
	}
	e.b.WriteString(")")
}

func (e *emitter) function(i int) {
	fn := &e.m.Funcs[i]
	e.fn = fn
	e.fnIndex = i
	e.prototype(i)
	fmt.Fprintf(&e.b, " { // %s\n", fn.Name)
	e.depth = 1
	e.temps = 0
	e.stmts(fn.Body.Stmts)
	e.b.WriteString("}\n\n")
	e.fn = nil
}

// entryShell prints an entry point's source: the params buffer at the
// register after the entry's buffers, ahead of the common source,
// whose functions read the params row in place, then the shell: the
// buffers at u0 upward, the root constants as a constant buffer at
// b0, and the kernel function main with its workgroup and root
// signature, which calls the IR wrapper with the record words and the
// thread's grid position. A group entry's
// row thread count, which no built-in supplies, is d_rowthreads, and
// an index or span entry adds d_base, the first thread of a piece of a
// split dispatch, to its x. The device fills both (layout.go).
func (e *emitter) entryShell(i int, en compile.Entry) (EntryPoint, error) {
	fn := &e.m.Funcs[en.Func]
	layout := EntryLayout(e.m, i)
	if err := layout.check(); err != nil {
		return EntryPoint{}, err
	}
	var b strings.Builder
	// The params buffer ahead of the functions, which read the params
	// row in place. An entry without params never reaches those reads,
	// so its declaration is unused and outside the root signature.
	fmt.Fprintf(&b, "RWByteAddressBuffer pp : register(u%d);\n", len(en.Buffers))
	b.WriteString(e.b.String())
	var args []string
	for _, bb := range en.Buffers {
		fmt.Fprintf(&b, "%s b%d : register(u%d);\n", e.typ(bb.Type), bb.Binding, bb.Binding)
		args = append(args, fmt.Sprintf("b%d", bb.Binding))
		// The length word the host supplies is the binding's word
		// count; a byte buffer's hidden count is its bytes.
		switch {
		case bb.Type.Dynamic && bb.Type.Word:
			args = append(args, fmt.Sprintf("d_len%d", bb.Binding))
		case bb.Type.Dynamic:
			args = append(args, fmt.Sprintf("d_len%d * 4u", bb.Binding))
		}
	}
	if len(en.Params) > 0 {
		args = append(args, "pp")
	}
	b.WriteString("cbuffer Dispatch : register(b0) {\n\tuint d_bx; uint d_by; uint d_off; uint d_stride; uint d_seg; uint d_rowthreads; uint d_base;\n")
	for j := 0; j < e.lengths; j++ {
		fmt.Fprintf(&b, "\tuint d_len%d;\n", j)
	}
	b.WriteString("};\n\n")
	args = append(args, "d_bx", "d_by", "d_off", "d_stride", "d_seg")
	if en.Group {
		args = append(args, "dtid.x", "dtid.y", "d_rowthreads")
	} else {
		args = append(args, "(dtid.x + d_base)", "dtid.y", "0u")
	}
	fmt.Fprintf(&b, "[numthreads(%d, %d, 1)]\n", layout.Workgroup[0], layout.Workgroup[1])
	fmt.Fprintf(&b, "[RootSignature(\"%s\")]\n", layout.RootSignature())
	fmt.Fprintf(&b, "void main(uint3 dtid : SV_DispatchThreadID) { // %s\n", fn.Name)
	fmt.Fprintf(&b, "\tfn%d(%s);\n}\n", en.Wrapper, strings.Join(args, ", "))
	return EntryPoint{Entry: i, Func: fn.Name, Source: b.String(), Layout: layout}, nil
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

// stmt prints a statement, with the aggregate literals its
// expressions hold hoisted ahead of it.
func (e *emitter) stmt(s compile.Stmt) {
	e.hoist(s)
	switch s := s.(type) {
	case *compile.Block:
		e.line("{")
		e.block(s)
		e.line("}")
	case *compile.VarDecl:
		l := e.fn.Locals[s.Local]
		name := fmt.Sprintf("l%d", s.Local)
		if c, ok := s.Init.(*compile.Call); ok && outResults(&e.m.Funcs[c.Func]) {
			e.line("%s;", e.decl(l.Type, name))
			e.line("%s;", e.callWithOut(c, []string{name}))
			return
		}
		if e.aggregateChain(s.Init) {
			e.line("%s;", e.decl(l.Type, name))
			e.selectInto(name, s.Init)
			return
		}
		e.line("%s = %s;", e.decl(l.Type, name), e.expr(s.Init))
		if ls, ok := s.Init.(*compile.LoadStruct); ok {
			// The row's address, for the reads in place.
			e.line("rt_prow = %s;", e.expr(ls.Addr))
		}
	case *compile.Assign:
		if c, ok := s.Values[0].(*compile.Call); ok && outResults(&e.m.Funcs[c.Func]) {
			e.line("%s;", e.callWithOut(c, e.lvalues(s.Targets)))
			return
		}
		target, value := s.Targets[0], s.Values[0]
		if e.chainIndex(target) != nil {
			// One branch of the chain runs, so the value is evaluated
			// once; a value other than a name is a temporary ahead of
			// the chain, so it is printed once.
			v := e.expr(value)
			if !e.named(value) {
				name := e.tempName()
				e.line("%s = %s;", e.decl(value.Type(), name), v)
				v = name
			}
			e.selectStore(target, v)
			return
		}
		if e.aggregateChain(value) {
			if _, ok := value.Type().(compile.ArrayType); ok && e.paramsInPlace(value) {
				// The row's array loads an element at a time, each
				// address reading the indexes again, and an index or
				// the target may read the target, so the elements go
				// to a temporary assigned to the target once.
				name := e.tempName()
				e.line("%s;", e.decl(value.Type(), name))
				e.selectInto(name, value)
				e.line("%s = %s;", e.lvalue(target), name)
				return
			}
			e.selectInto(e.lvalue(target), value)
			return
		}
		e.line("%s = %s;", e.lvalue(target), e.expr(value))
	case *compile.ExprStmt:
		if r, ok := s.X.(*compile.OpCall); ok && !r.Op.Yields {
			// An operation with no result prints its own line.
			e.operation(r)
			return
		}
		e.line("%s;", e.expr(s.X))
	case *compile.If:
		e.ifChain(s)
	case *compile.Switch:
		tag := e.expr(s.Tag)
		if s.Tag.Type() == compile.Scalar(compile.Bool) {
			tag = "((uint)" + wrapped(tag) + ")"
		}
		e.line("switch %s {", wrapped(tag))
		for _, c := range s.Cases {
			for _, v := range c.Values {
				e.line("case %s:", e.caseValue(v))
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
		if (s.Cond != nil && e.needsHoist(s.Cond)) || (s.Post != nil && !e.simplePost(s.Post)) {
			e.generalLoop(s)
			return
		}
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
		switch {
		case len(s.Values) == 0:
			e.line("return;")
		case !outResults(e.fn):
			e.line("return %s;", e.expr(s.Values[0]))
		default:
			// Results evaluate before any out parameter is written.
			for i, v := range s.Values {
				e.line("%s = %s;", e.decl(e.fn.Results[i], fmt.Sprintf("rv%d", i)), e.expr(v))
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

// The chain form of a run-time index. The compiler keeps an aggregate
// in registers only when every index into it is a constant, and an
// access at an index known only at run time places the whole aggregate
// in per-thread scratch memory, written in full before its first read.
// An access path rooted in a local, through arrays of at most selectMax
// elements at side-effect-free indexes, is printed over the constant
// indexes instead, so no index into the aggregate is left at run time:
// a scalar read as a select chain, an aggregate read as an if chain
// assigning a local, and a write as an if chain. An index out of the
// array's range takes the last element, where the indexed form's
// result was undefined. A reference argument the callee only reads is
// a copy taken through the chain; one it writes through and a loop
// post statement keep the indexed form. A read in an entry's params row
// is read in place instead (paramsInPlace).
const (
	selectMax    = 8  // the longest array a run-time index into is chained
	selectCombos = 64 // the most element paths one access expands to
)

// chainIndex is the run-time index of an access path that takes the
// chain form, the one nearest the path's root, or nil when the path
// keeps the indexed form: its root is not a local or a hoisted
// temporary, an index has side effects, an array is longer than
// selectMax, or the run-time indexes expand to more than selectCombos
// element paths.
func (e *emitter) chainIndex(x compile.Expr) *compile.IndexRef {
	var at *compile.IndexRef
	combos := 1
	for {
		if _, ok := e.hoisted[x]; ok {
			return at
		}
		switch n := x.(type) {
		case *compile.LocalRef:
			return at
		case *compile.FieldRef:
			x = n.X
		case *compile.IndexRef:
			if _, ok := n.Index.(*compile.Const); !ok {
				a, ok := n.X.Type().(compile.ArrayType)
				if !ok || a.Len > selectMax || !e.pure(n.Index) {
					return nil
				}
				combos *= a.Len
				if combos > selectCombos {
					return nil
				}
				at = n
			}
			x = n.X
		default:
			return nil
		}
	}
}

// pure reports an expression without side effects, which a chain may
// evaluate once per element.
func (e *emitter) pure(x compile.Expr) bool {
	switch x := x.(type) {
	case *compile.Const, *compile.LocalRef:
		return true
	case *compile.FieldRef:
		return e.pure(x.X)
	case *compile.IndexRef:
		return e.pure(x.X) && e.pure(x.Index)
	case *compile.TableRef:
		return e.pure(x.Index)
	case *compile.Unary:
		return e.pure(x.X)
	case *compile.Binary:
		return e.pure(x.X) && e.pure(x.Y)
	case *compile.Convert:
		return e.pure(x.X)
	case *compile.Mask:
		return e.pure(x.X)
	case *compile.OpCall:
		if !x.Op.Pure() {
			return false
		}
		for _, a := range x.Args {
			if !e.pure(a) {
				return false
			}
		}
		return true
	}
	return false
}

// atIndex is the access path x with the index at replaced by the
// constant k, the nodes below at shared with x.
func atIndex(x compile.Expr, at *compile.IndexRef, k int) compile.Expr {
	switch n := x.(type) {
	case *compile.IndexRef:
		if n == at {
			return &compile.IndexRef{T: n.T, X: n.X, Index: &compile.Const{T: n.Index.Type(), Value: constant.MakeInt64(int64(k))}}
		}
		return &compile.IndexRef{T: n.T, X: atIndex(n.X, at, k), Index: n.Index}
	case *compile.FieldRef:
		return &compile.FieldRef{T: n.T, X: atIndex(n.X, at, k), Field: n.Field}
	}
	return x
}

// isScalar reports a scalar type, the type a select chain carries.
func isScalar(t compile.Type) bool {
	_, ok := t.(compile.Scalar)
	return ok
}

// chainCond is the condition that the index at holds k.
func (e *emitter) chainCond(at *compile.IndexRef, k int) string {
	return fmt.Sprintf("(%s == %s)", wrapped(e.expr(at.Index)), e.constant(at.Index.Type(), constant.MakeInt64(int64(k))))
}

// chainLen is the element count of the array the index at selects in.
func chainLen(at *compile.IndexRef) int {
	return at.X.Type().(compile.ArrayType).Len
}

// selectRead prints a scalar read through the run-time index at as a
// select chain, the last element the fallback.
func (e *emitter) selectRead(x compile.Expr, at *compile.IndexRef) string {
	n := chainLen(at)
	s := e.expr(atIndex(x, at, n-1))
	for k := n - 2; k >= 0; k-- {
		s = fmt.Sprintf("select(%s, %s, %s)", e.chainCond(at, k), e.expr(atIndex(x, at, k)), s)
	}
	return s
}

// chain prints an if chain over the elements the index at selects,
// leaf printing each element's statements.
func (e *emitter) chain(at *compile.IndexRef, leaf func(k int)) {
	n := chainLen(at)
	for k := 0; k < n; k++ {
		switch {
		case k == 0:
			e.line("if %s {", e.chainCond(at, k))
		case k < n-1:
			e.line("} else if %s {", e.chainCond(at, k))
		default:
			e.line("} else {")
		}
		e.depth++
		leaf(k)
		e.depth--
	}
	e.line("}")
}

// selectInto assigns the value of the access path x to dst, through an
// if chain over each run-time index the path holds, or, for a path in
// the params row, loaded in place.
func (e *emitter) selectInto(dst string, x compile.Expr) {
	if e.paramsInPlace(x) {
		e.paramsInto(dst, x.Type(), e.paramsAddr(x))
		return
	}
	at := e.chainIndex(x)
	if at == nil {
		e.line("%s = %s;", dst, e.expr(x))
		return
	}
	e.chain(at, func(k int) { e.selectInto(dst, atIndex(x, at, k)) })
}

// selectStore assigns v to the access path target, through an if chain
// over each run-time index the path holds.
func (e *emitter) selectStore(target compile.Expr, v string) {
	at := e.chainIndex(target)
	if at == nil {
		e.line("%s = %s;", e.lvalue(target), v)
		return
	}
	e.chain(at, func(k int) { e.selectStore(atIndex(target, at, k), v) })
}

// aggregateChain reports an aggregate read in the chain form, which
// has no expression form and is assigned through selectInto.
func (e *emitter) aggregateChain(x compile.Expr) bool {
	switch x.(type) {
	case *compile.FieldRef, *compile.IndexRef:
		return !isScalar(x.Type()) && e.chainIndex(x) != nil
	}
	return false
}

// tempSelect declares a temporary for an aggregate read in the chain
// form, assigns it through the chain, and records its name.
func (e *emitter) tempSelect(x compile.Expr) {
	name := e.tempName()
	e.line("%s;", e.decl(x.Type(), name))
	e.selectInto(name, x)
	e.record(x)
}

// hoistPath hoists what the indexes of an access path need, the path's
// own nodes kept in place.
func (e *emitter) hoistPath(x compile.Expr) {
	if _, ok := e.hoisted[x]; ok {
		return
	}
	switch n := x.(type) {
	case *compile.FieldRef:
		e.hoistPath(n.X)
	case *compile.IndexRef:
		e.hoistPath(n.X)
		e.hoistExpr(n.Index, false)
	default:
		e.hoistExpr(x, false)
	}
}

// lvalue prints an assignment target or a reference argument in the
// indexed form, its indexes as expressions, or a read-only reference
// argument as its hoisted copy.
func (e *emitter) lvalue(x compile.Expr) string {
	if name, ok := e.hoisted[x]; ok {
		return name
	}
	switch n := x.(type) {
	case *compile.FieldRef:
		return fmt.Sprintf("%s.f%d", e.lvalue(n.X), n.Field)
	case *compile.IndexRef:
		return fmt.Sprintf("%s[%s]", e.lvalue(n.X), e.expr(n.Index))
	}
	return e.expr(x)
}

// lvalues prints a list of targets.
func (e *emitter) lvalues(list []compile.Expr) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = e.lvalue(a)
	}
	return out
}

// The params row read in place. A read from an entry's params row at a
// run-time index is a load from the params buffer, pp, declared ahead
// of the functions, at the row's address, rt_prow, which the wrapper
// sets when it loads the row. The wrapper's copy of the row holds every
// element of an array read this way as a live value per thread, which
// the chain form would read in full; the load reads the one element.
// A read at a constant path keeps the copy.

// paramsInPlace reports an access path rooted in a reference to an
// entry's params row that holds a run-time index.
func (e *emitter) paramsInPlace(x compile.Expr) bool {
	root := rootLocal(x)
	if root < 0 || !e.rowLocals()[[2]int{e.fnIndex, root}] {
		return false
	}
	if e.fn.Locals[root].Kind != compile.RefParam {
		return false
	}
	for {
		switch n := x.(type) {
		case *compile.FieldRef:
			x = n.X
		case *compile.IndexRef:
			if _, ok := n.Index.(*compile.Const); !ok {
				return true
			}
			x = n.X
		default:
			return false
		}
	}
}

// rowLocals is the locals, by function and index, that are an entry's
// params row: a wrapper's local loaded from the params buffer, and a
// reference parameter every call of its function passes a row local
// whole. A copy of the row is not the row: its fields may change.
func (e *emitter) rowLocals() map[[2]int]bool {
	if e.rows != nil {
		return e.rows
	}
	e.rows = map[[2]int]bool{}
	type site struct{ caller, local int }
	sites := map[[2]int][]site{} // by callee and parameter: the caller's argument local, -1 when not a local
	for f := range e.m.Funcs {
		walkCalls(e.m.Funcs[f].Body, func(c *compile.Call) {
			for i, a := range c.Args {
				if !e.isRef(c, i) {
					continue
				}
				l := -1
				if r, ok := a.(*compile.LocalRef); ok {
					l = r.Index
				}
				sites[[2]int{c.Func, i}] = append(sites[[2]int{c.Func, i}], site{f, l})
			}
		})
		walkStmts(e.m.Funcs[f].Body, func(s compile.Stmt) {
			if d, ok := s.(*compile.VarDecl); ok {
				if _, ok := d.Init.(*compile.LoadStruct); ok {
					e.rows[[2]int{f, d.Local}] = true
				}
			}
		})
	}
	for changed := true; changed; {
		changed = false
		for key, list := range sites {
			if e.rows[key] {
				continue
			}
			all := true
			for _, s := range list {
				if s.local < 0 || !e.rows[[2]int{s.caller, s.local}] {
					all = false
					break
				}
			}
			if all {
				e.rows[key] = true
				changed = true
			}
		}
	}
	return e.rows
}

// walkStmts calls fn for every statement of a block, nested ones
// included.
func walkStmts(b *compile.Block, fn func(compile.Stmt)) {
	for _, s := range b.Stmts {
		fn(s)
		switch s := s.(type) {
		case *compile.Block:
			walkStmts(s, fn)
		case *compile.If:
			walkStmts(s.Then, fn)
			for el := s.Else; el != nil; {
				fn(el)
				switch x := el.(type) {
				case *compile.Block:
					walkStmts(x, fn)
					el = nil
				case *compile.If:
					walkStmts(x.Then, fn)
					el = x.Else
				default:
					el = nil
				}
			}
		case *compile.Switch:
			for _, c := range s.Cases {
				walkStmts(c.Body, fn)
			}
			if s.Default != nil {
				walkStmts(s.Default, fn)
			}
		case *compile.For:
			if s.Post != nil {
				fn(s.Post)
			}
			walkStmts(s.Body, fn)
		}
	}
}

// walkCalls calls fn for every call in a block's statements.
func walkCalls(b *compile.Block, fn func(*compile.Call)) {
	var expr func(x compile.Expr)
	expr = func(x compile.Expr) {
		switch x := x.(type) {
		case *compile.FieldRef:
			expr(x.X)
		case *compile.IndexRef:
			expr(x.X)
			expr(x.Index)
		case *compile.TableRef:
			expr(x.Index)
		case *compile.Composite:
			for _, el := range x.Elems {
				expr(el)
			}
		case *compile.Call:
			fn(x)
			for _, a := range x.Args {
				expr(a)
			}
		case *compile.OpCall:
			for _, a := range x.Args {
				expr(a)
			}
		case *compile.Unary:
			expr(x.X)
		case *compile.Binary:
			expr(x.X)
			expr(x.Y)
		case *compile.Convert:
			expr(x.X)
		case *compile.Mask:
			expr(x.X)
		case *compile.LoadStruct:
			expr(x.Buffer)
			expr(x.Addr)
		}
	}
	walkStmts(b, func(s compile.Stmt) {
		switch s := s.(type) {
		case *compile.VarDecl:
			expr(s.Init)
		case *compile.Assign:
			for _, t := range s.Targets {
				expr(t)
			}
			for _, v := range s.Values {
				expr(v)
			}
		case *compile.ExprStmt:
			expr(s.X)
		case *compile.If:
			expr(s.Cond)
		case *compile.Switch:
			expr(s.Tag)
		case *compile.For:
			if s.Cond != nil {
				expr(s.Cond)
			}
		case *compile.Return:
			for _, v := range s.Values {
				expr(v)
			}
		}
	})
}

// paramsAddr is the byte address in pp of an access path rooted in the
// params row: rt_prow, the constant offsets, and each run-time index by
// its element size.
func (e *emitter) paramsAddr(x compile.Expr) string {
	off := 0
	var parts []string
	for done := false; !done; {
		switch n := x.(type) {
		case *compile.FieldRef:
			off += e.m.Structs[n.X.Type().(compile.StructRef).Index].Fields[n.Field].Offset
			x = n.X
		case *compile.IndexRef:
			stride := e.size(n.T)
			if c, ok := n.Index.(*compile.Const); ok {
				k, _ := constant.Int64Val(constant.ToInt(c.Value))
				off += int(k) * stride
			} else {
				parts = append(parts, fmt.Sprintf("((uint)%s * %du)", wrapped(e.expr(n.Index)), stride))
			}
			x = n.X
		default:
			done = true
		}
	}
	s := fmt.Sprintf("rt_prow + %du", off)
	for _, p := range parts {
		s += " + " + p
	}
	return "(" + s + ")"
}

// paramsLoad is the load of a scalar from pp at addr, in the loaders'
// forms.
func (e *emitter) paramsLoad(t compile.Scalar, addr string) string {
	switch compile.Kind(t) {
	case compile.Bool:
		return fmt.Sprintf("(rt_u8(pp, %s, 0xffffffffu) != 0u)", addr)
	case compile.I32:
		return fmt.Sprintf("asint(pp.Load(%s))", addr)
	case compile.U32:
		return fmt.Sprintf("pp.Load(%s)", addr)
	case compile.F32:
		return fmt.Sprintf("asfloat(pp.Load(%s))", addr)
	case compile.I64:
		return fmt.Sprintf("((int64_t)rt_load64(pp, %s))", addr)
	case compile.U64:
		return fmt.Sprintf("rt_load64(pp, %s)", addr)
	}
	e.fail("params field of type %s has no buffer form", t)
	return "?"
}

// paramsInto assigns dst the value of type t at addr in pp: a scalar
// load, an element at a time for an array, the loader for a struct.
func (e *emitter) paramsInto(dst string, t compile.Type, addr string) {
	switch u := t.(type) {
	case compile.Scalar:
		e.line("%s = %s;", dst, e.paramsLoad(u, addr))
	case compile.ArrayType:
		stride := e.size(u.Elem)
		for i := 0; i < u.Len; i++ {
			e.paramsInto(fmt.Sprintf("%s[%d]", dst, i), u.Elem, fmt.Sprintf("(%s + %du)", addr, i*stride))
		}
	case compile.StructRef:
		e.line("%s = ld%d(pp, %s);", dst, u.Index, addr)
	default:
		e.fail("params field of type %s has no buffer form", t)
	}
}

// named reports an expression that prints as a name or a constant.
func (e *emitter) named(x compile.Expr) bool {
	if _, ok := e.hoisted[x]; ok {
		return true
	}
	switch x.(type) {
	case *compile.LocalRef, *compile.Const:
		return true
	}
	return false
}

// isRef reports whether argument i of a call is passed by reference.
func (e *emitter) isRef(c *compile.Call, i int) bool {
	fn := &e.m.Funcs[c.Func]
	return i < fn.NumParams && fn.Locals[i].Kind == compile.RefParam
}

// readRef reports a reference argument the chain form takes: an access
// path in the chain form passed to a parameter its function never
// writes through, so a temporary copy of the element stands for it.
func (e *emitter) readRef(c *compile.Call, i int) bool {
	return e.isRef(c, i) && e.chainIndex(c.Args[i]) != nil && !e.writesRef(c.Func, i)
}

// rootLocal is the local an access path is rooted in, or -1.
func rootLocal(x compile.Expr) int {
	for {
		switch n := x.(type) {
		case *compile.LocalRef:
			return n.Index
		case *compile.FieldRef:
			x = n.X
		case *compile.IndexRef:
			x = n.X
		default:
			return -1
		}
	}
}

// writesRef reports whether function f writes through its reference
// parameter p: an assignment to a path rooted in it, or the parameter
// passed on by reference to a function that writes through it. The
// call graph is acyclic, so callees resolve first.
func (e *emitter) writesRef(f, p int) bool {
	key := [2]int{f, p}
	if w, ok := e.refWrites[key]; ok {
		return w
	}
	if e.refWrites == nil {
		e.refWrites = map[[2]int]bool{}
	}
	written := false
	body := e.m.Funcs[f].Body
	walkStmts(body, func(s compile.Stmt) {
		if a, ok := s.(*compile.Assign); ok {
			for _, t := range a.Targets {
				if rootLocal(t) == p {
					written = true
				}
			}
		}
	})
	walkCalls(body, func(c *compile.Call) {
		for i, a := range c.Args {
			if e.isRef(c, i) && rootLocal(a) == p && e.writesRef(c.Func, i) {
				written = true
			}
		}
	})
	e.refWrites[key] = written
	return written
}

// caseValue prints a switch case constant; a bool case is the word
// the bool tag was cast to.
func (e *emitter) caseValue(v compile.Expr) string {
	if c, ok := v.(*compile.Const); ok && c.T == compile.Scalar(compile.Bool) {
		if constant.BoolVal(c.Value) {
			return "1u"
		}
		return "0u"
	}
	return e.expr(v)
}

// hoist declares, ahead of the statement and in evaluation order,
// the temporaries its expressions need: an aggregate literal outside
// an initializer position, and a call with out results outside its
// statement form, since neither is an expression in HLSL. It records
// the name each prints as. The initializer positions are a VarDecl's
// initializer and the values of a return through out parameters,
// which declare temporaries; a literal nested in another prints
// inline as a nested brace list. The statement forms of an
// out-result call are a VarDecl or a single-target Assign whose
// whole value is the call, which stmt prints as the call with the
// target as the out argument.
func (e *emitter) hoist(s compile.Stmt) {
	e.hoisted = nil
	var exprs []compile.Expr
	inline := false
	switch s := s.(type) {
	case *compile.VarDecl:
		if c, ok := s.Init.(*compile.Call); ok && outResults(&e.m.Funcs[c.Func]) {
			e.hoistArgs(c)
			return
		}
		if e.aggregateChain(s.Init) {
			// The chain assigns the local itself (stmt).
			e.hoistPath(s.Init)
			return
		}
		inline = true
		exprs = []compile.Expr{s.Init}
	case *compile.Assign:
		for _, t := range s.Targets {
			e.hoistPath(t)
		}
		value := s.Values[0]
		if c, ok := value.(*compile.Call); ok && outResults(&e.m.Funcs[c.Func]) {
			e.hoistArgs(c)
			return
		}
		if e.aggregateChain(value) && e.chainIndex(s.Targets[0]) == nil {
			// The chain assigns the target itself (stmt).
			e.hoistPath(value)
			return
		}
		exprs = s.Values
	case *compile.ExprStmt:
		exprs = []compile.Expr{s.X}
	case *compile.If:
		exprs = []compile.Expr{s.Cond}
	case *compile.Switch:
		exprs = []compile.Expr{s.Tag}
	case *compile.Return:
		inline = outResults(e.fn)
		exprs = s.Values
	}
	for _, x := range exprs {
		e.hoistExpr(x, inline)
	}
}

// hoistExpr walks an expression in evaluation order and hoists every
// aggregate literal not in an initializer position and every call
// with out results.
func (e *emitter) hoistExpr(x compile.Expr, initializer bool) {
	switch x := x.(type) {
	case *compile.Composite:
		for _, el := range x.Elems {
			e.hoistExpr(el, true)
		}
		if !initializer {
			e.temp(x)
		}
	case *compile.Zero:
		if !initializer {
			e.temp(x)
		}
	case *compile.FieldRef, *compile.IndexRef:
		if e.chainIndex(x) != nil {
			// A scalar prints as a select chain in place; an aggregate
			// has no expression form and is a temporary.
			e.hoistPath(x)
			if !isScalar(x.Type()) {
				e.tempSelect(x)
			}
			return
		}
		switch x := x.(type) {
		case *compile.FieldRef:
			e.hoistExpr(x.X, false)
		case *compile.IndexRef:
			e.hoistExpr(x.X, false)
			e.hoistExpr(x.Index, false)
		}
	case *compile.TableRef:
		e.hoistExpr(x.Index, false)
	case *compile.Call:
		e.hoistArgs(x)
		if outResults(&e.m.Funcs[x.Func]) {
			e.tempCall(x)
		}
	case *compile.OpCall:
		for _, a := range x.Args {
			e.hoistExpr(a, false)
		}
	case *compile.Unary:
		e.hoistExpr(x.X, false)
	case *compile.Binary:
		e.hoistExpr(x.X, false)
		e.hoistExpr(x.Y, false)
	case *compile.Convert:
		e.hoistExpr(x.X, false)
	case *compile.Mask:
		e.hoistExpr(x.X, false)
	case *compile.LoadStruct:
		e.hoistExpr(x.Buffer, false)
		e.hoistExpr(x.Addr, false)
	}
}

// hoistArgs hoists what a call's arguments need, a reference argument
// as the place it names.
func (e *emitter) hoistArgs(c *compile.Call) {
	for i, a := range c.Args {
		switch {
		case e.readRef(c, i):
			// A temporary copy of the element stands for it.
			e.hoistPath(a)
			e.tempSelect(a)
		case e.isRef(c, i):
			e.hoistPath(a)
		default:
			e.hoistExpr(a, false)
		}
	}
}

// temp declares a hoisted literal as a temporary and records its name.
func (e *emitter) temp(x compile.Expr) {
	e.line("%s = %s;", e.decl(x.Type(), e.tempName()), e.literal(x))
	e.record(x)
}

// tempCall declares a temporary for an out-result call's array
// result, filled by the call in its statement form, and records its
// name.
func (e *emitter) tempCall(x *compile.Call) {
	name := e.tempName()
	e.line("%s;", e.decl(x.Type(), name))
	e.line("%s;", e.callWithOut(x, []string{name}))
	e.record(x)
}

// tempName is the next temporary's name.
func (e *emitter) tempName() string {
	name := fmt.Sprintf("h%d", e.temps)
	e.temps++
	return name
}

// record maps a hoisted expression to the temporary just declared.
func (e *emitter) record(x compile.Expr) {
	if e.hoisted == nil {
		e.hoisted = map[compile.Expr]string{}
	}
	e.hoisted[x] = fmt.Sprintf("h%d", e.temps-1)
}

// needsHoist reports an expression holding an aggregate literal or a
// call with out results.
func (e *emitter) needsHoist(x compile.Expr) bool {
	found := false
	var walk func(x compile.Expr)
	walk = func(x compile.Expr) {
		switch x := x.(type) {
		case *compile.Composite, *compile.Zero:
			found = true
		case *compile.FieldRef, *compile.IndexRef:
			if e.chainIndex(x) != nil {
				// The path prints whole: an aggregate needs a temporary,
				// and only the indexes are walked.
				if !isScalar(x.Type()) {
					found = true
				}
				for p := x; ; {
					if f, ok := p.(*compile.FieldRef); ok {
						p = f.X
					} else if i, ok := p.(*compile.IndexRef); ok {
						walk(i.Index)
						p = i.X
					} else {
						break
					}
				}
				return
			}
			switch x := x.(type) {
			case *compile.FieldRef:
				walk(x.X)
			case *compile.IndexRef:
				walk(x.X)
				walk(x.Index)
			}
		case *compile.TableRef:
			walk(x.Index)
		case *compile.Call:
			if outResults(&e.m.Funcs[x.Func]) {
				found = true
			}
			for i, a := range x.Args {
				if !e.isRef(x, i) {
					walk(a)
					continue
				}
				// A reference argument is a place: a read-only one in
				// the chain form is a temporary, and only its indexes
				// are walked.
				if e.readRef(x, i) {
					found = true
				}
				for p := a; ; {
					if f, ok := p.(*compile.FieldRef); ok {
						p = f.X
					} else if n, ok := p.(*compile.IndexRef); ok {
						walk(n.Index)
						p = n.X
					} else {
						break
					}
				}
			}
		case *compile.OpCall:
			for _, a := range x.Args {
				walk(a)
			}
		case *compile.Unary:
			walk(x.X)
		case *compile.Binary:
			walk(x.X)
			walk(x.Y)
		case *compile.Convert:
			walk(x.X)
		case *compile.Mask:
			walk(x.X)
		}
	}
	walk(x)
	return found
}

// ifChain prints an if with else-if continuation. An else-if whose
// condition needs a hoisted literal is printed as a nested if in an
// else block, so the temporary has a statement position.
func (e *emitter) ifChain(s *compile.If) {
	e.line("if %s {", wrapped(e.expr(s.Cond)))
	e.block(s.Then)
	switch el := s.Else.(type) {
	case nil:
		e.line("}")
	case *compile.Block:
		e.line("} else {")
		e.block(el)
		e.line("}")
	case *compile.If:
		if e.needsHoist(el.Cond) {
			e.line("} else {")
			e.depth++
			e.stmt(el)
			e.depth--
			e.line("}")
			return
		}
		e.b.WriteString(strings.Repeat("\t", e.depth))
		e.b.WriteString("} else ")
		saved := e.depth
		e.depth = 0
		e.hoist(el)
		e.ifChain(el)
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

// simplePost reports a loop post statement the for header can hold
// (simple): an assignment or an expression statement that needs no
// temporary, as a temporary has no statement position there.
func (e *emitter) simplePost(s compile.Stmt) bool {
	switch s := s.(type) {
	case *compile.Assign:
		// A target is a place printed in the indexed form (lvalue), so
		// only its indexes can need a temporary; a value can anywhere.
		exprs := append([]compile.Expr{}, s.Values...)
		if c, ok := s.Values[0].(*compile.Call); ok && outResults(&e.m.Funcs[c.Func]) {
			exprs = append([]compile.Expr{}, c.Args...)
		}
		for _, t := range s.Targets {
			for p := t; ; {
				if f, ok := p.(*compile.FieldRef); ok {
					p = f.X
				} else if n, ok := p.(*compile.IndexRef); ok {
					exprs = append(exprs, n.Index)
					p = n.X
				} else {
					break
				}
			}
		}
		for _, x := range exprs {
			if e.needsHoist(x) {
				return false
			}
		}
		return true
	case *compile.ExprStmt:
		return !e.needsHoist(s.X)
	}
	return false
}

// simple prints a loop post statement simplePost admits, without
// indentation or the terminating semicolon.
func (e *emitter) simple(s compile.Stmt) string {
	switch s := s.(type) {
	case *compile.Assign:
		if c, ok := s.Values[0].(*compile.Call); ok && outResults(&e.m.Funcs[c.Func]) {
			return e.callWithOut(c, e.lvalues(s.Targets))
		}
		return e.lvalue(s.Targets[0]) + " = " + e.expr(s.Values[0])
	case *compile.ExprStmt:
		return e.expr(s.X)
	}
	e.fail("unsupported loop post statement %T", s)
	return ""
}

// generalLoop prints a loop whose condition or post needs a temporary,
// which the for header has no statement position for. The header's
// increment sets a flag that runs the post at the top of every pass
// but the first, so a continue reaches the post as in the for header,
// and the condition, its temporaries ahead of it, breaks when false.
func (e *emitter) generalLoop(s *compile.For) {
	flag := e.tempName()
	e.line("for (bool %s = false; ; %s = true) {", flag, flag)
	e.depth++
	if s.Post != nil {
		e.line("if (%s) {", flag)
		e.depth++
		e.stmt(s.Post)
		e.depth--
		e.line("}")
	}
	if s.Cond != nil {
		e.stmt(&compile.If{
			Cond: &compile.Unary{T: compile.Scalar(compile.Bool), Op: compile.Not, X: s.Cond},
			Then: &compile.Block{Stmts: []compile.Stmt{&compile.Break{}}},
		})
	}
	e.depth--
	e.block(s.Body)
	e.line("}")
}

// callWithOut prints a call whose results are out parameters, with
// the targets as the out arguments.
func (e *emitter) callWithOut(c *compile.Call, targets []string) string {
	args := append(e.callArgs(c), targets...)
	return fmt.Sprintf("fn%d(%s)", c.Func, strings.Join(args, ", "))
}

// callArgs prints a call's arguments, a variable-length buffer's
// hidden word count after the buffer.
func (e *emitter) callArgs(c *compile.Call) []string {
	var out []string
	for i, a := range c.Args {
		if e.isRef(c, i) {
			out = append(out, e.lvalue(a))
		} else {
			out = append(out, e.expr(a))
		}
		if l, ok := a.(*compile.LocalRef); ok {
			if bt, ok := l.T.(compile.BufferType); ok && bt.Dynamic {
				out = append(out, fmt.Sprintf("l%d_n", l.Index))
			}
		}
	}
	return out
}

func (e *emitter) args(list []compile.Expr) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = e.expr(a)
	}
	return out
}

func (e *emitter) expr(x compile.Expr) string {
	if name, ok := e.hoisted[x]; ok {
		return name
	}
	switch x := x.(type) {
	case *compile.Const:
		return e.constant(x.T, x.Value)
	case *compile.LocalRef:
		return fmt.Sprintf("l%d", x.Index)
	case *compile.FieldRef:
		if s, ok := x.T.(compile.Scalar); ok && e.paramsInPlace(x) {
			return e.paramsLoad(s, e.paramsAddr(x))
		}
		if at := e.chainIndex(x); at != nil && isScalar(x.T) {
			return e.selectRead(x, at)
		}
		return fmt.Sprintf("%s.f%d", e.expr(x.X), x.Field)
	case *compile.IndexRef:
		if s, ok := x.T.(compile.Scalar); ok && e.paramsInPlace(x) {
			return e.paramsLoad(s, e.paramsAddr(x))
		}
		if at := e.chainIndex(x); at != nil && isScalar(x.T) {
			return e.selectRead(x, at)
		}
		return fmt.Sprintf("%s[%s]", e.expr(x.X), e.expr(x.Index))
	case *compile.TableRef:
		return fmt.Sprintf("tb%d[%s]", x.Table, e.expr(x.Index))
	case *compile.Zero, *compile.Composite:
		return e.literal(x)
	case *compile.Call:
		if outResults(&e.m.Funcs[x.Func]) {
			e.fail("call of %s with out results in expression position", e.m.Funcs[x.Func].Name)
			return "?"
		}
		return fmt.Sprintf("fn%d(%s)", x.Func, strings.Join(e.callArgs(x), ", "))
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
		return e.convert(x.T.(compile.Scalar), x.X)
	case *compile.Mask:
		return e.mask(x.T.(compile.Scalar), e.expr(x.X))
	case *compile.LoadStruct:
		return fmt.Sprintf("ld%d(%s, %s)", x.T.(compile.StructRef).Index, e.expr(x.Buffer), e.expr(x.Addr))
	}
	e.fail("unsupported expression %T", x)
	return "?"
}

// wrapTypes is the signed HLSL type of a signed scalar and the
// unsigned type of its register width, through which its add,
// subtract, multiply, negate, and shift left are printed: the
// compiler marks signed arithmetic as never overflowing and folds
// comparisons on that basis, while Go wraps, and unsigned arithmetic
// wraps by definition, with the conversion back two's complement.
// Narrow types share int's register, so their operations run on uint
// before the Mask.
func wrapTypes(t compile.Type) (signed, unsigned string, ok bool) {
	s, isScalar := t.(compile.Scalar)
	if !isScalar {
		return "", "", false
	}
	switch compile.Kind(s) {
	case compile.I8, compile.I16, compile.I32:
		return "int", "uint", true
	case compile.I64:
		return "int64_t", "uint64_t", true
	}
	return "", "", false
}

// literal prints an aggregate literal as a brace list: a composite's
// elements, or a zero value's zero constants over its type.
func (e *emitter) literal(x compile.Expr) string {
	switch x := x.(type) {
	case *compile.Composite:
		return "{" + strings.Join(e.args(x.Elems), ", ") + "}"
	case *compile.Zero:
		return e.zeroList(x.T)
	}
	e.fail("literal of %T", x)
	return "?"
}

// zeroList prints the zero value of a type as a brace list.
func (e *emitter) zeroList(t compile.Type) string {
	switch u := t.(type) {
	case compile.Scalar:
		return e.constant(u, zeroConst(u))
	case compile.ArrayType:
		parts := make([]string, u.Len)
		for i := range parts {
			parts[i] = e.zeroList(u.Elem)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case compile.StructRef:
		st := e.m.Structs[u.Index]
		parts := make([]string, len(st.Fields))
		for i, f := range st.Fields {
			parts[i] = e.zeroList(f.Type)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	e.fail("zero value of type %s", t)
	return "?"
}

// zeroConst is a scalar's zero as a constant value.
func zeroConst(s compile.Scalar) constant.Value {
	switch compile.Kind(s) {
	case compile.Bool:
		return constant.MakeBool(false)
	case compile.F32:
		return constant.MakeFloat64(0)
	}
	return constant.MakeInt64(0)
}

// mask re-canonicalizes a narrow value held in a 32-bit register:
// sign-extension for signed, the shift left on uint (wrapTypes) and
// the shift right arithmetic on int, and an AND for unsigned.
func (e *emitter) mask(t compile.Scalar, x string) string {
	switch compile.Kind(t) {
	case compile.I8:
		return "((int)((uint)" + wrapped(x) + " << 24) >> 24)"
	case compile.I16:
		return "((int)((uint)" + wrapped(x) + " << 16) >> 16)"
	case compile.U8:
		return "(" + x + " & 0xffu)"
	case compile.U16:
		return "(" + x + " & 0xffffu)"
	}
	return x
}

// convert prints a conversion as a cast; a narrow target
// canonicalizes. A signed 32-bit register widened to the unsigned
// 64-bit type goes through the signed one, so it sign-extends as Go
// does.
func (e *emitter) convert(t compile.Scalar, x compile.Expr) string {
	src := e.expr(x)
	switch compile.Kind(t) {
	case compile.I8, compile.I16:
		return e.mask(t, src)
	case compile.U8, compile.U16:
		return e.mask(t, "((uint)"+wrapped(src)+")")
	case compile.U64:
		if s, ok := x.Type().(compile.Scalar); ok {
			switch compile.Kind(s) {
			case compile.I8, compile.I16, compile.I32:
				return "((uint64_t)((int64_t)" + wrapped(src) + "))"
			}
		}
	}
	return "((" + e.typ(t) + ")" + wrapped(src) + ")"
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
// buffer, which takes the hidden word count after the buffer. A
// variable-length buffer must be a word buffer for a word operation
// and a byte buffer otherwise.
func (e *emitter) access(buf *compile.LocalRef, name string, word bool, args ...string) string {
	bt := buf.T.(compile.BufferType)
	b := e.expr(buf)
	if bt.Dynamic {
		if bt.Word != word {
			kind := "byte"
			if bt.Word {
				kind = "word"
			}
			e.fail("%s on a variable %s buffer", name, kind)
			return "?"
		}
		args = append([]string{b, b + "_n"}, args...)
		return "rt_view_" + strings.TrimPrefix(name, "rt_") + "(" + strings.Join(args, ", ") + ")"
	}
	args = append(append([]string{b}, args...), fmt.Sprintf("%du", bt.Size-1))
	return name + "(" + strings.Join(args, ", ") + ")"
}

// The renderer operations: buffer operations through the prelude,
// integer operations through the intrinsics at 32 bits and the
// prelude at 64, and the phase boundary as the device memory barrier
// with group sync.

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

func (e *emitter) Length(buf *compile.LocalRef) string { return e.expr(buf) + "_n" }

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

func (e *emitter) MinI64(x, y string) string { return "rt_min_i64(" + x + ", " + y + ")" }

func (e *emitter) MaxI64(x, y string) string { return "rt_max_i64(" + x + ", " + y + ")" }

func (e *emitter) MinU32(x, y string) string { return "min(" + x + ", " + y + ")" }

func (e *emitter) MaxU32(x, y string) string { return "max(" + x + ", " + y + ")" }

func (e *emitter) NextPhase() { e.line("DeviceMemoryBarrierWithGroupSync();") }

// constant prints a typed constant. A signed 32-bit value carries
// the L suffix, which under the HLSL 2021 the build compiles with
// types a decimal literal as int; without a suffix the compiler
// holds it as a 64-bit literal, and a literal left of a shift by a
// variable count is typed by the count. The compiler reads a literal
// that starts with 0 as octal and types a suffixed octal or hex
// literal as uint, so zero is printed without the suffix: it takes
// the type of the operand beside it, which for zero changes no
// value. A 64-bit value is composed from its halves through casts
// rather than a literal suffix.
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
		if n >= math.MinInt32 && n <= math.MaxInt32 {
			return "((int64_t)" + int32Literal(int32(n)) + ")"
		}
		return fmt.Sprintf("((int64_t)(((uint64_t)(%du) << 32) | (uint64_t)(%du)))", uint32(n>>32), uint32(n))
	case compile.U64:
		n, _ := constant.Uint64Val(constant.ToInt(v))
		if n <= math.MaxUint32 {
			return fmt.Sprintf("((uint64_t)(%du))", n)
		}
		return fmt.Sprintf("(((uint64_t)(%du) << 32) | (uint64_t)(%du))", uint32(n>>32), uint32(n))
	case compile.U8, compile.U16, compile.U32:
		n, _ := constant.Uint64Val(constant.ToInt(v))
		return fmt.Sprintf("%du", n)
	default:
		n, _ := constant.Int64Val(constant.ToInt(v))
		return int32Literal(int32(n))
	}
}

// int32Literal prints a signed 32-bit constant, the minimum as an
// expression since its magnitude is not a literal, and zero without
// the suffix, since 0L is an octal literal and so unsigned.
func int32Literal(n int32) string {
	if n == 0 {
		return "0"
	}
	if n == math.MinInt32 {
		return "(-2147483647L - 1L)"
	}
	if n < 0 {
		return fmt.Sprintf("(%dL)", n)
	}
	return fmt.Sprintf("%dL", n)
}

// formatFloat prints a float32 exactly enough to round-trip.
func formatFloat(f float32) string {
	s := fmt.Sprintf("%.9g", f)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
