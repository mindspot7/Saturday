// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"runtime"
)

// Lower lowers a validated Program to the IR. Every diagnostic of the
// run is returned together; a Module is returned only when there are
// none.
func Lower(prog *Program) (*Module, error) {
	lw := &lowerer{
		prog:    prog,
		diag:    &diagnostics{fset: prog.Fset},
		mod:     &Module{},
		sizes:   types.SizesFor("gc", runtime.GOARCH),
		structs: map[*types.Named]int{},
		tables:  map[*types.Var]int{},
		funcs:   map[*types.Func]int{},
		buffers: map[*types.TypeName]BufferType{},
		groups:  map[*types.TypeName]*groupLayout{},
	}
	lw.collectBuffers()
	lw.collectGroups(prog.Parent)
	for _, pkg := range prog.Packages {
		lw.collectGroups(pkg)
	}
	for _, pkg := range prog.Packages {
		lw.pkg = pkg
		lw.collectStructs()
		lw.collectTables()
		lw.declareFuncs()
	}
	for _, pkg := range prog.Packages {
		lw.pkg = pkg
		for _, fd := range funcDecls(pkg.Files) {
			lw.lowerFunc(fd)
		}
		lw.lowerEntries()
	}
	if err := lw.diag.err(); err != nil {
		return nil, err
	}
	return lw.mod, nil
}

type lowerer struct {
	prog    *Program
	diag    *diagnostics
	mod     *Module
	sizes   types.Sizes
	structs map[*types.Named]int
	tables  map[*types.Var]int
	funcs   map[*types.Func]int
	buffers map[*types.TypeName]BufferType
	groups  map[*types.TypeName]*groupLayout

	// Per package.
	pkg *Package

	// Per function.
	fn      *Func
	locals  map[*types.Var]int
	fields  map[*types.Var][]int // a buffer group parameter's buffer locals
	results []int                // locals holding named results
	blocks  []*Block             // the blocks being built, innermost last
}

// groupLayout is a buffer group flattened to its buffers: every
// buffer the group holds, depth first in field order, and where each
// field's buffers start and how many it holds. An array field holds
// one buffer per element; a nested group field holds its group's.
type groupLayout struct {
	buffers []groupBuffer
	fields  []groupSlot
}

type groupBuffer struct {
	name string
	typ  BufferType
}

type groupSlot struct {
	first, count int
}

// collectBuffers records the parent's buffer types.
func (lw *lowerer) collectBuffers() {
	scope := lw.prog.Parent.Types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		switch u := tn.Type().Underlying().(type) {
		case *types.Slice:
			word := u.Elem().Underlying().(*types.Basic).Kind() == types.Uint32
			lw.buffers[tn] = BufferType{Name: name, Word: word, Dynamic: true}
		case *types.Array:
			word := u.Elem().Underlying().(*types.Basic).Kind() == types.Uint32
			lw.buffers[tn] = BufferType{Name: name, Word: word, Size: int(u.Len())}
		}
	}
}

// collectGroups flattens a package's buffer groups. A group lowers to
// no type of its own: a parameter of the group expands to one buffer
// parameter per buffer it holds, and a path to one of them is that
// buffer's parameter. The validator has established which structs
// are groups: those whose fields all hold buffers.
func (lw *lowerer) collectGroups(pkg *Package) {
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		if tn, ok := scope.Lookup(name).(*types.TypeName); ok {
			lw.groupOf(tn)
		}
	}
}

// groupOf is a struct type's flattened layout, or nil when the type is
// not a buffer group.
func (lw *lowerer) groupOf(tn *types.TypeName) *groupLayout {
	if g, ok := lw.groups[tn]; ok {
		return g
	}
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 {
		return nil
	}
	g := &groupLayout{}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		slot := groupSlot{first: len(g.buffers)}
		t := types.Unalias(f.Type())
		n := 1
		suffix := func(int) string { return "" }
		if arr, ok := t.(*types.Array); ok {
			t, n = types.Unalias(arr.Elem()), int(arr.Len())
			suffix = func(k int) string { return fmt.Sprintf("[%d]", k) }
		}
		if p, ok := t.(*types.Pointer); ok {
			t = types.Unalias(p.Elem())
		}
		named, ok := t.(*types.Named)
		if !ok {
			return nil
		}
		for k := 0; k < n; k++ {
			if bt, ok := lw.buffers[named.Obj()]; ok {
				g.buffers = append(g.buffers, groupBuffer{name: f.Name() + suffix(k), typ: bt})
				continue
			}
			sub := lw.groupOf(named.Obj())
			if sub == nil {
				return nil
			}
			for _, b := range sub.buffers {
				g.buffers = append(g.buffers, groupBuffer{name: f.Name() + "." + b.name, typ: b.typ})
			}
		}
		slot.count = len(g.buffers) - slot.first
		g.fields = append(g.fields, slot)
	}
	lw.groups[tn] = g
	return g
}

// group is the buffer group a type names, directly or by pointer, or
// nil.
func (lw *lowerer) group(t types.Type) *groupLayout {
	if named := namedOf(t); named != nil {
		return lw.groups[named.Obj()]
	}
	return nil
}

// namedOf is the named type t is or points to, aliases resolved, or
// nil: the form the buffer and group tables are keyed by.
func namedOf(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	named, _ := t.(*types.Named)
	return named
}

// elementOf is t with one array level removed, aliases resolved: an
// array of buffers holds buffers.
func elementOf(t types.Type) types.Type {
	t = types.Unalias(t)
	if a, ok := t.(*types.Array); ok {
		return a.Elem()
	}
	return t
}

// collectStructs registers the package's named struct types in
// declaration order, so struct indices are stable across functions.
func (lw *lowerer) collectStructs() {
	for _, f := range lw.pkg.Files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				n, ok := lw.pkg.Info.Defs[ts.Name].Type().(*types.Named)
				if !ok {
					continue
				}
				if _, ok := n.Underlying().(*types.Struct); ok && lw.groups[n.Obj()] == nil {
					lw.structIndex(n)
				}
			}
		}
	}
}

// collectTables lowers the package's lookup tables.
func (lw *lowerer) collectTables() {
	for _, f := range lw.pkg.Files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					obj := lw.pkg.Info.Defs[n].(*types.Var)
					lit := vs.Values[i].(*ast.CompositeLit)
					at := lw.typ(obj.Type()).(ArrayType)
					values := make([]constant.Value, at.Len)
					for j := range values {
						values[j] = zeroConst(at.Elem.(Scalar))
					}
					idx := 0
					for _, el := range lit.Elts {
						val := el
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							k, _ := constant.Int64Val(constant.ToInt(lw.pkg.Info.Types[kv.Key].Value))
							idx = int(k)
							val = kv.Value
						}
						values[idx] = lw.pkg.Info.Types[val].Value
						idx++
					}
					lw.tables[obj] = len(lw.mod.Tables)
					lw.mod.Tables = append(lw.mod.Tables, Table{Name: n.Name, Type: at, Values: values})
				}
			}
		}
	}
}

// declareFuncs assigns every function of the package its index so
// calls can refer to functions declared later.
func (lw *lowerer) declareFuncs() {
	for _, fd := range funcDecls(lw.pkg.Files) {
		obj := lw.pkg.Info.Defs[fd.Name].(*types.Func)
		lw.funcs[obj] = len(lw.mod.Funcs)
		name := fd.Name.Name
		if fd.Recv != nil {
			name = lw.recvName(obj) + "." + name
		}
		lw.mod.Funcs = append(lw.mod.Funcs, Func{Name: name})
	}
}

func (lw *lowerer) recvName(obj *types.Func) string {
	t := obj.Type().(*types.Signature).Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	return types.Unalias(t).(*types.Named).Obj().Name()
}

// typ maps a Go type to its IR type. Pointers map to their target,
// since references are a parameter kind rather than a type.
func (lw *lowerer) typ(t types.Type) Type {
	t = types.Unalias(t)
	switch u := t.(type) {
	case *types.Pointer:
		return lw.typ(u.Elem())
	case *types.Named:
		if b, ok := lw.buffers[u.Obj()]; ok {
			return b
		}
		if _, ok := lw.groups[u.Obj()]; ok {
			lw.diag.add(token.NoPos, "buffer group %s used as a type", u.Obj().Name())
			return Scalar(I32)
		}
		if _, ok := u.Underlying().(*types.Struct); ok {
			return StructRef{Index: lw.structIndex(u)}
		}
		return lw.typ(u.Underlying())
	case *types.Basic:
		s, ok := scalarOf(u)
		if !ok {
			lw.diag.add(token.NoPos, "unexpected basic type %s", u)
		}
		return s
	case *types.Array:
		return ArrayType{Elem: lw.typ(u.Elem()), Len: int(u.Len())}
	case *types.Tuple:
		tt := Tuple{}
		for i := 0; i < u.Len(); i++ {
			tt.Elems = append(tt.Elems, lw.typ(u.At(i).Type()))
		}
		return tt
	}
	lw.diag.add(token.NoPos, "unexpected type %s", t)
	return Scalar(I32)
}

// scalarOf maps a basic type, reporting a kind the subset has no
// scalar for. Implementation-sized and untyped integer kinds appear
// only as constants (indices, shift counts) and lower to 32-bit.
func scalarOf(b *types.Basic) (Scalar, bool) {
	switch b.Kind() {
	case types.Bool, types.UntypedBool:
		return Scalar(Bool), true
	case types.Int8:
		return Scalar(I8), true
	case types.Int16:
		return Scalar(I16), true
	case types.Int32, types.Int, types.UntypedInt, types.UntypedRune:
		return Scalar(I32), true
	case types.Int64:
		return Scalar(I64), true
	case types.Uint8:
		return Scalar(U8), true
	case types.Uint16:
		return Scalar(U16), true
	case types.Uint32, types.Uint:
		return Scalar(U32), true
	case types.Uint64:
		return Scalar(U64), true
	case types.Float32, types.UntypedFloat:
		return Scalar(F32), true
	}
	return Scalar(I32), false
}

// narrowed reports a constant of an implementation-sized or untyped
// integer kind whose value does not fit the 32 bits scalarOf lowers
// the kind to, where Go holds it at 64.
func narrowed(t types.Type, v constant.Value) bool {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	switch b.Kind() {
	case types.Int, types.UntypedInt, types.UntypedRune:
		n, exact := constant.Int64Val(constant.ToInt(v))
		return !exact || n < math.MinInt32 || n > math.MaxInt32
	case types.Uint:
		n, exact := constant.Uint64Val(constant.ToInt(v))
		return !exact || n > math.MaxUint32
	}
	return false
}

// structIndex registers a named struct type, laying it out, and
// returns its index.
func (lw *lowerer) structIndex(n *types.Named) int {
	if i, ok := lw.structs[n]; ok {
		return i
	}
	// Reserve the index before laying out, so the struct keeps its
	// place ahead of the nested structs its layout registers. The
	// emitters print structs in dependency order.
	i := len(lw.mod.Structs)
	lw.structs[n] = i
	lw.mod.Structs = append(lw.mod.Structs, StructType{Name: n.Obj().Name()})
	st := lw.layout(n)
	lw.mod.Structs[i] = st
	return i
}

// layout applies the layout rule: natural alignment, no reordering,
// size rounded to the struct's alignment. The result is verified
// against the Go compiler's own offsets for the same struct.
func (lw *lowerer) layout(n *types.Named) StructType {
	gs := n.Underlying().(*types.Struct)
	st := StructType{Name: n.Obj().Name(), Align: 1}
	off := 0
	fields := make([]*types.Var, gs.NumFields())
	for i := range fields {
		f := gs.Field(i)
		fields[i] = f
		ft := lw.typ(f.Type())
		size, align := lw.sizeAlign(ft)
		off = alignUp(off, align)
		st.Fields = append(st.Fields, Field{Name: f.Name(), Type: ft, Offset: off})
		off += size
		if align > st.Align {
			st.Align = align
		}
	}
	st.Size = alignUp(off, st.Align)
	goOffsets := lw.sizes.Offsetsof(fields)
	for i, f := range st.Fields {
		if int(goOffsets[i]) != f.Offset {
			lw.diag.add(fields[i].Pos(), "struct %s field %s: layout rule offset %d differs from the Go offset %d", st.Name, f.Name, f.Offset, goOffsets[i])
		}
	}
	if goSize := int(lw.sizes.Sizeof(n)); goSize != st.Size {
		lw.diag.add(n.Obj().Pos(), "struct %s: layout rule size %d differs from the Go size %d", st.Name, st.Size, goSize)
	}
	return st
}

// sizeAlign returns a type's size and alignment under the layout rule.
func (lw *lowerer) sizeAlign(t Type) (size, align int) {
	switch u := t.(type) {
	case Scalar:
		if Kind(u) == Bool {
			return 1, 1
		}
		return u.Width() / 8, u.Width() / 8
	case ArrayType:
		es, ea := lw.sizeAlign(u.Elem)
		return alignUp(es, ea) * u.Len, ea
	case StructRef:
		st := lw.mod.Structs[u.Index]
		return st.Size, st.Align
	}
	lw.diag.add(token.NoPos, "no layout for %s", t)
	return 4, 4
}

func alignUp(n, a int) int { return (n + a - 1) / a * a }

// zeroConst is the zero constant of a scalar type.
func zeroConst(s Scalar) constant.Value {
	switch Kind(s) {
	case Bool:
		return constant.MakeBool(false)
	case F32:
		return constant.MakeFloat64(0)
	}
	return constant.MakeInt64(0)
}

// zero is the zero value of a type as an expression.
func zero(t Type) Expr {
	if s, ok := t.(Scalar); ok {
		return &Const{T: s, Value: zeroConst(s)}
	}
	return &Zero{T: t}
}

// lowerEntries builds the entry records for the package's named entry
// points and synthesizes each one's wrapper.
func (lw *lowerer) lowerEntries() {
	for _, name := range lw.pkg.Entries {
		obj := lw.pkg.Types.Scope().Lookup(name).(*types.Func)
		fi := lw.funcs[obj]
		fn := lw.mod.Funcs[fi]
		e := Entry{Func: fi}
		for i := 0; i < fn.NumParams; i++ {
			l := fn.Locals[i]
			switch l.Kind {
			case BufferParam:
				e.Buffers = append(e.Buffers, BufferBinding{Param: i, Type: l.Type.(BufferType), Binding: len(e.Buffers)})
			case RefParam:
				e.Params = append(e.Params, i)
			default:
				e.Coords++
			}
		}
		e.Span = e.Coords == 2
		e.Group = e.Coords == 3
		e.Wrapper = lw.synthesizeWrapper(fn, fi, &e)
		lw.mod.Entries = append(lw.mod.Entries, e)
	}
}

// synthesizeWrapper builds the entry's wrapper function: buffers,
// the params buffer, the dispatch record fields, and the thread
// coordinates in; the bound guards, the row's params loaded at
// paramsOffset + gy*paramsStride, a fresh row state, and the call
// with the segment range (span entries), the index, or the thread's
// index in its group, the group's size, and the group's index (group
// entries, whose shell passes them as gx, extra, and gy).
//
// Only the entry's first struct parameter is loaded: it is the params
// row. A second is row state, which the thread starts at its zero
// value, as the software renderer does at the start of a row. The
// validator's checkParamsReadOnly holds that order, since an entry
// written the other way round would read a zeroed params row here
// and run correctly in software.
func (lw *lowerer) synthesizeWrapper(fn Func, fi int, e *Entry) int {
	w := Func{Name: fn.Name + ".wrapper"}
	u32 := Scalar(U32)
	local := func(name string, t Type, kind ParamKind) int {
		w.Locals = append(w.Locals, Local{Name: name, Type: t, Kind: kind})
		return len(w.Locals) - 1
	}
	ref := func(li int) Expr { return &LocalRef{T: w.Locals[li].Type, Index: li} }
	bin := func(op Op, x, y Expr) Expr { return &Binary{T: u32, Op: op, X: x, Y: y} }
	ge := func(x, y Expr) Expr { return &Binary{T: Scalar(Bool), Op: Ge, X: x, Y: y} }

	args := make([]Expr, fn.NumParams)
	for _, bb := range e.Buffers {
		args[bb.Param] = ref(local(fn.Locals[bb.Param].Name, bb.Type, BufferParam))
	}
	pbuf := -1
	if len(e.Params) > 0 {
		pbuf = local("params", paramsBuffer, BufferParam)
	}
	boundX := local("boundX", u32, ValueParam)
	boundY := local("boundY", u32, ValueParam)
	offset := local("paramsOffset", u32, ValueParam)
	stride := local("paramsStride", u32, ValueParam)
	segment := local("segment", u32, ValueParam)
	gx := local("gx", u32, ValueParam)
	gy := local("gy", u32, ValueParam)
	extra := local("extra", u32, ValueParam)
	w.NumParams = len(w.Locals)

	ret := &Block{Stmts: []Stmt{&Return{}}}
	body := &Block{}
	body.Stmts = append(body.Stmts, &If{Cond: ge(ref(gy), ref(boundY)), Then: ret})
	coordBase := fn.NumParams - e.Coords
	// coord passes the U32 local src as coordinate i, converted when
	// the entry declares the coordinate int32.
	coord := func(i int, src int) {
		args[coordBase+i] = ref(src)
		if fn.Locals[coordBase+i].Type != u32 {
			args[coordBase+i] = &Convert{T: fn.Locals[coordBase+i].Type, X: ref(src)}
		}
	}
	if e.Span {
		x0 := local("x0", u32, ValueParam)
		body.Stmts = append(body.Stmts, &VarDecl{Local: x0, Init: bin(Mul, ref(gx), ref(segment))})
		body.Stmts = append(body.Stmts, &If{Cond: ge(ref(x0), ref(boundX)), Then: ret})
		x1 := local("x1", u32, ValueParam)
		body.Stmts = append(body.Stmts, &VarDecl{Local: x1, Init: &OpCall{T: u32, Op: opMinU32, Args: []Expr{bin(Add, ref(x0), ref(segment)), ref(boundX)}}})
		coord(0, x0)
		coord(1, x1)
	} else {
		coord(0, gx)
		if e.Group {
			coord(1, extra)
			coord(2, gy)
		} else {
			body.Stmts = append(body.Stmts, &If{Cond: ge(ref(gx), ref(boundX)), Then: ret})
		}
	}
	for j, p := range e.Params {
		t := fn.Locals[p].Type
		li := local(fn.Locals[p].Name, t, ValueParam)
		var init Expr = zero(t)
		if j == 0 {
			init = &LoadStruct{T: t, Buffer: ref(pbuf), Addr: bin(Add, ref(offset), bin(Mul, ref(gy), ref(stride)))}
		}
		body.Stmts = append(body.Stmts, &VarDecl{Local: li, Init: init})
		args[p] = ref(li)
	}
	body.Stmts = append(body.Stmts, &ExprStmt{X: &Call{Func: fi, Args: args}})
	w.Body = body
	lw.mod.Funcs = append(lw.mod.Funcs, w)
	return len(lw.mod.Funcs) - 1
}

// lowerFunc lowers one function declaration into its reserved slot.
func (lw *lowerer) lowerFunc(fd *ast.FuncDecl) {
	obj := lw.pkg.Info.Defs[fd.Name].(*types.Func)
	sig := obj.Type().(*types.Signature)
	fi := lw.funcs[obj]
	lw.fn = &lw.mod.Funcs[fi]
	lw.locals = map[*types.Var]int{}
	lw.fields = map[*types.Var][]int{}
	lw.results = nil

	if recv := sig.Recv(); recv != nil {
		lw.addParam(recv)
	}
	for i := 0; i < sig.Params().Len(); i++ {
		lw.addParam(sig.Params().At(i))
	}
	lw.fn.NumParams = len(lw.fn.Locals)
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		lw.fn.Results = append(lw.fn.Results, lw.typ(r.Type()))
	}

	body := &Block{}
	lw.blocks = []*Block{body}
	// Named results are locals initialized to zero at entry, when the
	// body refers to them or returns bare; a function that only ever
	// returns explicit values never reads them. A blank result is a
	// temporary, which a bare return returns at its zero value.
	if lw.usesNamedResults(fd, sig) {
		for i := 0; i < sig.Results().Len(); i++ {
			r := sig.Results().At(i)
			var li int
			if r.Name() == "_" {
				li = lw.temp(lw.typ(r.Type()))
			} else {
				li = lw.newLocal(r, r.Name())
			}
			lw.results = append(lw.results, li)
			lw.emit(&VarDecl{Local: li, Init: zero(lw.fn.Locals[li].Type)})
		}
	}
	lw.stmts(fd.Body.List)
	lw.fn.Body = body
	lw.blocks = nil
	lw.fn = nil
}

// usesNamedResults reports whether a function body reads or assigns a
// named result or returns bare. A bare return names every result,
// the blank ones included, so the walk runs whenever the function has
// results, even when none can be referred to by name.
func (lw *lowerer) usesNamedResults(fd *ast.FuncDecl, sig *types.Signature) bool {
	if sig.Results().Len() == 0 {
		return false
	}
	results := map[types.Object]bool{}
	for i := 0; i < sig.Results().Len(); i++ {
		if r := sig.Results().At(i); r.Name() != "" && r.Name() != "_" {
			results[r] = true
		}
	}
	used := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if results[lw.pkg.Info.Uses[x]] {
				used = true
			}
		case *ast.ReturnStmt:
			if len(x.Results) == 0 {
				used = true
			}
		}
		return !used
	})
	return used
}

// addParam declares a parameter local with its passing kind. A buffer
// group parameter declares one buffer local per buffer the group
// holds, in the group's order, named for the parameter and the path
// to the buffer.
func (lw *lowerer) addParam(p *types.Var) {
	if g := lw.group(p.Type()); g != nil {
		lw.locals[p] = len(lw.fn.Locals)
		for _, b := range g.buffers {
			lw.fields[p] = append(lw.fields[p], len(lw.fn.Locals))
			lw.fn.Locals = append(lw.fn.Locals, Local{Name: p.Name() + "." + b.name, Type: b.typ, Kind: BufferParam})
		}
		return
	}
	kind := ValueParam
	t := p.Type()
	if ptr, ok := t.(*types.Pointer); ok {
		kind = RefParam
		t = ptr.Elem()
	}
	if named, ok := types.Unalias(t).(*types.Named); ok {
		if _, ok := lw.buffers[named.Obj()]; ok {
			kind = BufferParam
		}
	}
	li := len(lw.fn.Locals)
	lw.fn.Locals = append(lw.fn.Locals, Local{Name: p.Name(), Type: lw.typ(p.Type()), Kind: kind})
	lw.locals[p] = li
}

// newLocal declares a local for a Go variable.
func (lw *lowerer) newLocal(v *types.Var, name string) int {
	li := len(lw.fn.Locals)
	lw.locals[v] = li
	lw.fn.Locals = append(lw.fn.Locals, Local{Name: name, Type: lw.typ(v.Type())})
	return li
}

// temp declares an unnamed local of the given type.
func (lw *lowerer) temp(t Type) int {
	li := len(lw.fn.Locals)
	lw.fn.Locals = append(lw.fn.Locals, Local{Name: "", Type: t})
	return li
}

// emit appends a statement to the block being built.
func (lw *lowerer) emit(s Stmt) {
	b := lw.blocks[len(lw.blocks)-1]
	b.Stmts = append(b.Stmts, s)
}

// push starts building a new block; pop finishes it.
func (lw *lowerer) push() *Block {
	b := &Block{}
	lw.blocks = append(lw.blocks, b)
	return b
}

func (lw *lowerer) pop() {
	lw.blocks = lw.blocks[:len(lw.blocks)-1]
}

// localOf returns the local index of a Go variable.
func (lw *lowerer) localOf(id *ast.Ident) (int, bool) {
	obj, ok := lw.pkg.Info.Uses[id].(*types.Var)
	if !ok {
		obj, ok = lw.pkg.Info.Defs[id].(*types.Var)
	}
	if !ok {
		return 0, false
	}
	li, ok := lw.locals[obj]
	return li, ok
}

func (lw *lowerer) typeOf(e ast.Expr) Type {
	return lw.typ(lw.pkg.Info.Types[e].Type)
}
