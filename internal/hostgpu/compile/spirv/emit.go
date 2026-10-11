// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// EntryPoint describes one emitted entry point, at the index of the
// module's entry it emits: the buffer bindings in order, the params
// binding (-1 when the entry takes none), the workgroup, whether the
// entry is a group entry, whose grid the host never splits, whether
// the push block carries word counts, the push block's size in bytes,
// and the module. The module's entry point is named main.
type EntryPoint struct {
	Buffers       []compile.BufferBinding
	ParamsBinding int
	Workgroup     [2]int
	Group         bool
	BufferLengths bool
	PushSize      int
	Code          []uint32
}

// Output is one module per entry point.
type Output struct {
	Entries []EntryPoint
}

// The push block, a contract between the shell and the vulkan device
// alone: the dispatch record's five words, then ThreadBaseWord, the
// first thread of a piece of a split index or span dispatch, zero for
// an unsplit one. PushWords is the fixed part's count. The word counts
// of an entry with a variable-length buffer follow, one per data
// buffer binding.
const (
	ThreadBaseWord = 5
	PushWords      = 6
)

// Emit lowers every entry point of the module to its own SPIR-V
// module holding the functions the entry reaches.
func Emit(m *compile.Module) (*Output, error) {
	e := &emitter{m: m, written: make([][]bool, len(m.Funcs))}
	out := &Output{}
	for _, en := range m.Entries {
		k, err := e.entry(en)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, k)
	}
	return out, nil
}

// emitter holds the analyses shared by every entry's module: which
// reference parameters each function writes through, and which locals
// are an entry's params row (rowLocals).
type emitter struct {
	m       *compile.Module
	written [][]bool
	rows    map[[2]int]bool
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
	for fi := range e.m.Funcs {
		walk(e.m.Funcs[fi].Body, func(s compile.Stmt) {
			if d, ok := s.(*compile.VarDecl); ok {
				if _, ok := d.Init.(*compile.LoadStruct); ok {
					e.rows[[2]int{fi, d.Local}] = true
				}
			}
		}, func(x compile.Expr) {
			c, ok := x.(*compile.Call)
			if !ok {
				return
			}
			callee := &e.m.Funcs[c.Func]
			for j, a := range c.Args {
				if callee.Locals[j].Kind != compile.RefParam {
					continue
				}
				l := -1
				if r, ok := a.(*compile.LocalRef); ok {
					l = r.Index
				}
				sites[[2]int{c.Func, j}] = append(sites[[2]int{c.Func, j}], site{fi, l})
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

// writes reports, per parameter of the function, whether the
// function writes through it when it is a reference parameter: an
// assignment targets a path rooted at it, or it, or a path into it,
// is passed to a callee's parameter that is written. The call graph
// is acyclic, so the callees resolve first.
func (e *emitter) writes(fi int) []bool {
	if w := e.written[fi]; w != nil {
		return w
	}
	fn := &e.m.Funcs[fi]
	w := make([]bool, fn.NumParams)
	e.written[fi] = w
	mark := func(x compile.Expr) {
		if r, ok := root(x); ok && r < fn.NumParams && fn.Locals[r].Kind == compile.RefParam {
			w[r] = true
		}
	}
	walk(fn.Body, func(s compile.Stmt) {
		if a, ok := s.(*compile.Assign); ok {
			for _, t := range a.Targets {
				mark(t)
			}
		}
	}, func(x compile.Expr) {
		if c, ok := x.(*compile.Call); ok {
			callee := &e.m.Funcs[c.Func]
			cw := e.writes(c.Func)
			for j, a := range c.Args {
				if callee.Locals[j].Kind == compile.RefParam && cw[j] {
					mark(a)
				}
			}
		}
	})
	return w
}

// root is the local a field or index path is rooted at.
func root(x compile.Expr) (int, bool) {
	for {
		switch u := x.(type) {
		case *compile.LocalRef:
			return u.Index, true
		case *compile.FieldRef:
			x = u.X
		case *compile.IndexRef:
			x = u.X
		default:
			return 0, false
		}
	}
}

// walk visits every statement and expression of a block.
func walk(b *compile.Block, stmt func(compile.Stmt), expr func(compile.Expr)) {
	var vs func(s compile.Stmt)
	var ve func(x compile.Expr)
	ve = func(x compile.Expr) {
		if x == nil {
			return
		}
		expr(x)
		switch u := x.(type) {
		case *compile.FieldRef:
			ve(u.X)
		case *compile.IndexRef:
			ve(u.X)
			ve(u.Index)
		case *compile.TableRef:
			ve(u.Index)
		case *compile.Composite:
			for _, el := range u.Elems {
				ve(el)
			}
		case *compile.Call:
			for _, a := range u.Args {
				ve(a)
			}
		case *compile.OpCall:
			for _, a := range u.Args {
				ve(a)
			}
		case *compile.Unary:
			ve(u.X)
		case *compile.Binary:
			ve(u.X)
			ve(u.Y)
		case *compile.Convert:
			ve(u.X)
		case *compile.Mask:
			ve(u.X)
		case *compile.LoadStruct:
			ve(u.Buffer)
			ve(u.Addr)
		}
	}
	vs = func(s compile.Stmt) {
		if s == nil {
			return
		}
		stmt(s)
		switch u := s.(type) {
		case *compile.Block:
			for _, st := range u.Stmts {
				vs(st)
			}
		case *compile.VarDecl:
			ve(u.Init)
		case *compile.Assign:
			for _, t := range u.Targets {
				ve(t)
			}
			for _, v := range u.Values {
				ve(v)
			}
		case *compile.ExprStmt:
			ve(u.X)
		case *compile.If:
			ve(u.Cond)
			vs(u.Then)
			vs(u.Else)
		case *compile.Switch:
			ve(u.Tag)
			for _, c := range u.Cases {
				for _, v := range c.Values {
					ve(v)
				}
				vs(c.Body)
			}
			if u.Default != nil {
				vs(u.Default)
			}
		case *compile.For:
			ve(u.Cond)
			vs(u.Post)
			vs(u.Body)
		case *compile.Return:
			for _, v := range u.Values {
				ve(v)
			}
		}
	}
	vs(b)
}

// reachable is the set of functions the wrapper reaches through
// calls, the wrapper included.
func (e *emitter) reachable(wrapper int) []bool {
	reach := make([]bool, len(e.m.Funcs))
	var visit func(fi int)
	visit = func(fi int) {
		if reach[fi] {
			return
		}
		reach[fi] = true
		walk(e.m.Funcs[fi].Body, func(compile.Stmt) {}, func(x compile.Expr) {
			if c, ok := x.(*compile.Call); ok {
				visit(c.Func)
			}
		})
	}
	visit(wrapper)
	return reach
}

// module is one entry's module under construction: the builder, the
// declared functions, the table variables, the params buffer's
// variable, and the Private variable holding the params row's byte
// address, which the wrapper sets when it loads the row.
type module struct {
	e       *emitter
	b       *builder
	fns     []fnDecl // by function index; zero for a function the entry does not reach
	tables  []uint32 // Private variables by table index, declared on first use
	params  uint32   // the params buffer's variable, zero for an entry without params
	prowVar uint32   // declared on first use
}

// prow is the Private variable holding the params row's byte address.
func (m *module) prow() uint32 {
	if m.prowVar == 0 {
		m.prowVar = m.b.global(m.b.ptr(storagePrivate, m.b.tU32()), storagePrivate, 0)
		op(&m.b.debug, opName, append([]uint32{m.prowVar}, words("prow")...)...)
	}
	return m.prowVar
}

// fnDecl is a declared function: its id, its type, its result type
// (the void type for none), and its parameter types in order,
// hidden length parameters included.
type fnDecl struct {
	id     uint32
	typ    uint32
	ret    uint32
	params []uint32
}

// entry builds the module of one entry point.
func (e *emitter) entry(en compile.Entry) (EntryPoint, error) {
	b := newBuilder()
	m := &module{e: e, b: b, fns: make([]fnDecl, len(e.m.Funcs)), tables: make([]uint32, len(e.m.Tables))}
	// The Vulkan memory model: a storage buffer access the barrier
	// orders carries the NonPrivatePointer operand (bufferLoad and
	// bufferStore), the barrier makes them available and visible
	// (NextPhase), and the atomics run at device scope.
	op(&b.caps, opCapability, capShader)
	op(&b.caps, opCapability, capInt64)
	op(&b.caps, opCapability, capStorageBuffer8BitAccess)
	op(&b.caps, opCapability, capVariablePointersStorageBuffer)
	op(&b.caps, opCapability, capVulkanMemoryModel)
	op(&b.caps, opCapability, capVulkanMemoryModelDeviceScope)
	op(&b.memory, opMemoryModel, addressingLogical, memoryVulkan)

	// The wrapper's params buffer parameter follows its data buffers.
	// Its variable is declared ahead of the functions, which read the
	// row in place through it.
	if len(en.Params) > 0 {
		binding := len(en.Buffers)
		wrapper := &e.m.Funcs[en.Wrapper]
		m.params = b.global(m.bufferPtr(wrapper.Locals[binding].Type.(compile.BufferType)), storageStorageBuffer, 0)
		op(&b.decor, opDecorate, m.params, decorationDescriptorSet, 0)
		op(&b.decor, opDecorate, m.params, decorationBinding, uint32(binding))
		op(&b.decor, opDecorate, m.params, decorationNonWritable)
	}

	reach := e.reachable(en.Wrapper)
	for fi := range e.m.Funcs {
		if reach[fi] {
			m.declare(fi)
		}
	}
	for fi := range e.m.Funcs {
		if reach[fi] {
			m.function(fi)
		}
	}
	k := m.shell(en)
	if b.err != nil {
		return EntryPoint{}, b.err
	}
	k.Code = b.assemble()
	return k, nil
}

// table is the Private variable of a lookup table, declared on first
// use and initialized with the table's constant values.
func (m *module) table(ti int) uint32 {
	if m.tables[ti] != 0 {
		return m.tables[ti]
	}
	b := m.b
	t := m.e.m.Tables[ti]
	elem := t.Type.Elem.(compile.Scalar)
	et := b.scalar(elem)
	elems := make([]uint32, len(t.Values))
	for i, v := range t.Values {
		elems[i] = m.constant(elem, v)
	}
	at := b.array(et, t.Type.Len)
	init := b.composite(at, elems)
	pt := b.ptr(storagePrivate, at)
	id := b.global(pt, storagePrivate, init)
	op(&b.debug, opName, append([]uint32{id}, words(t.Name)...)...)
	m.tables[ti] = id
	return id
}

// typeOf maps a value type: scalars to their register type, arrays
// and structs to undecorated composites, a tuple to a result struct.
func (m *module) typeOf(t compile.Type) uint32 {
	b := m.b
	switch u := t.(type) {
	case compile.Scalar:
		return b.scalar(u)
	case compile.ArrayType:
		return b.array(m.typeOf(u.Elem), u.Len)
	case compile.StructRef:
		st := m.e.m.Structs[u.Index]
		members := make([]uint32, len(st.Fields))
		for i, f := range st.Fields {
			members[i] = m.typeOf(f.Type)
		}
		return b.strct(members)
	case compile.Tuple:
		members := make([]uint32, len(u.Elems))
		for i, el := range u.Elems {
			members[i] = m.typeOf(el)
		}
		return b.strct(members)
	}
	b.fail("no value type for %s", t)
	return b.tU32()
}

// bufferBlock is the block type a buffer binds: bytes for a byte
// buffer, words for a word buffer and for the params buffer, which
// the wrapper reads a word at a time.
func (m *module) bufferBlock(bt compile.BufferType) uint32 {
	if bt.Word || bt.Params {
		return m.b.block(m.b.tU32(), 4)
	}
	return m.b.block(m.b.tU8(), 1)
}

// bufferPtr is the StorageBuffer pointer to a buffer's block.
func (m *module) bufferPtr(bt compile.BufferType) uint32 {
	return m.b.ptr(storageStorageBuffer, m.bufferBlock(bt))
}

// declare assigns a function its id and type: value parameters by
// value, reference parameters as Function pointers, buffers as
// StorageBuffer pointers each followed by a hidden word count when
// variable-length, and the results as one value or a result struct.
func (m *module) declare(fi int) {
	b := m.b
	fn := &m.e.m.Funcs[fi]
	d := fnDecl{id: b.id(), ret: b.tVoid()}
	for p := 0; p < fn.NumParams; p++ {
		l := fn.Locals[p]
		switch l.Kind {
		case compile.RefParam:
			d.params = append(d.params, b.ptr(storageFunction, m.typeOf(l.Type)))
		case compile.BufferParam:
			bt := l.Type.(compile.BufferType)
			d.params = append(d.params, m.bufferPtr(bt))
			if bt.Dynamic {
				d.params = append(d.params, b.tU32())
			}
		default:
			d.params = append(d.params, m.typeOf(l.Type))
		}
	}
	switch len(fn.Results) {
	case 0:
	case 1:
		d.ret = m.typeOf(fn.Results[0])
	default:
		d.ret = m.typeOf(compile.Tuple{Elems: fn.Results})
	}
	d.typ = b.fnType(d.ret, d.params)
	m.fns[fi] = d
	op(&b.debug, opName, append([]uint32{d.id}, words(fn.Name)...)...)
}
