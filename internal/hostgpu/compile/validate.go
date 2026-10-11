// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// validator checks the compute subpackages against the compute subset.
// It runs after type checking, so every identifier and expression type
// is resolved; it rejects constructs the subset excludes and reports
// each at its source position.
type validator struct {
	prog    *Program
	errs    *diagnostics
	buffers map[*types.TypeName]bool // the parent's buffer types and every package's groups
	groups  map[*types.TypeName]bool // the buffer groups

	// Per package.
	pkg      *Package
	tables   map[*types.Var]bool // package-level lookup arrays
	params   map[*types.Var]bool // parameters and receivers, which may be pointers
	calls    map[*types.Func][]*types.Func
	decl     map[*types.Func]token.Pos
	lenCalls map[ast.Expr]bool // len of a variable-length buffer, whose Go type is int

	// The pointer-parameter facts the params-row rule needs: where a
	// function writes through one of its pointer parameters, and
	// which parameter of a callee a pointer parameter reaches when it
	// is passed on, whole or as a path into it. The subset forbids
	// storing a pointer in a local, a struct, or an array, so a
	// pointer moves only as a call's argument or receiver and this
	// flow is the whole of it.
	ptrWrites map[*types.Var]token.Pos
	ptrFlow   map[*types.Var][]*types.Var
	// The calls that pass more than one reference, for the aliasing
	// rule.
	aliasCalls []aliasCall

	// Per function body.
	cur *types.Func
	// The NextPhase calls that are statements directly in a group
	// entry's body, the one placement every thread of the workgroup
	// reaches (checkBody).
	phases map[*ast.CallExpr]bool
}

func newValidator(prog *Program, errs *diagnostics) *validator {
	return &validator{prog: prog, errs: errs, buffers: map[*types.TypeName]bool{}, groups: map[*types.TypeName]bool{}}
}

// checkParent records the parent package's buffer types and rejects
// imports and declarations outside what the subset grants it:
// constants, byte or word buffer arrays and slices, buffer groups, and
// functions, with no imports. The compile stage compiles none of the
// parent's bodies: a call of a parent function is lowered to the
// operation of compute.Operations its name means (lowerer.operation),
// and the declarations are read only to type-check those calls.
func (v *validator) checkParent() {
	p := v.prog.Parent
	for _, f := range p.Files {
		v.checkFileComments(f)
		for _, imp := range f.Imports {
			v.errs.add(imp.Pos(), "parent compute package may not import %s", strings.Trim(imp.Path.Value, `"`))
		}
	}
	scope := p.Types.Scope()
	var structs []*types.TypeName
	for _, name := range scope.Names() {
		switch obj := scope.Lookup(name).(type) {
		case *types.TypeName:
			if bufferDecl(obj) {
				v.buffers[obj] = true
				continue
			}
			if _, ok := obj.Type().Underlying().(*types.Struct); ok {
				structs = append(structs, obj)
				continue
			}
			v.errs.add(obj.Pos(), "parent compute package type %s is not a byte or word buffer type or a buffer group", name)
		case *types.Var:
			v.errs.add(obj.Pos(), "parent compute package has no package-level variables (%s)", name)
		}
	}
	// The groups after the buffers, so a field's type is known
	// whatever the declaration order.
	for _, obj := range structs {
		if !v.resolveGroup(obj, map[*types.TypeName]bool{}) {
			v.errs.add(obj.Pos(), "parent compute package type %s is not a byte or word buffer type or a buffer group", obj.Name())
		}
	}
}

// bufferDecl reports a buffer type declaration: a fixed-size array of
// bytes or words, or a variable-length byte or word slice.
func bufferDecl(obj *types.TypeName) bool {
	switch u := obj.Type().Underlying().(type) {
	case *types.Slice:
		b, ok := u.Elem().Underlying().(*types.Basic)
		return ok && (b.Kind() == types.Uint8 || b.Kind() == types.Uint32)
	case *types.Array:
		b, ok := u.Elem().Underlying().(*types.Basic)
		return ok && (b.Kind() == types.Uint8 || b.Kind() == types.Uint32)
	}
	return false
}

// resolveGroup records tn as a buffer group when it is one: a struct
// whose every field is a group member. The walk follows nested groups
// and stops on a cycle, which is no group.
func (v *validator) resolveGroup(tn *types.TypeName, visiting map[*types.TypeName]bool) bool {
	if v.groups[tn] {
		return true
	}
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 || visiting[tn] {
		return false
	}
	visiting[tn] = true
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Embedded() || !v.groupMember(st.Field(i).Type(), visiting) {
			return false
		}
	}
	v.groups[tn] = true
	v.buffers[tn] = true
	return true
}

// groupMember reports a type a buffer group's field may have: a
// variable-length buffer by value, a fixed-size buffer or another
// group by pointer, or an array of buffers.
func (v *validator) groupMember(t types.Type, visiting map[*types.TypeName]bool) bool {
	t = types.Unalias(t)
	arr, isArray := t.(*types.Array)
	if isArray {
		t = types.Unalias(arr.Elem())
	}
	if p, ok := t.(*types.Pointer); ok {
		named, ok := types.Unalias(p.Elem()).(*types.Named)
		if !ok {
			return false
		}
		if v.isFixedBuffer(named) {
			return true
		}
		return !isArray && v.resolveGroup(named.Obj(), visiting)
	}
	return v.isSliceBufferType(t)
}

// mentionsBuffer reports a type that is, or points to, or is an array
// of, a buffer or a buffer group.
func (v *validator) mentionsBuffer(t types.Type) bool {
	named := namedOf(elementOf(t))
	return named != nil && v.buffers[named.Obj()]
}

// collectGroups records a subpackage's buffer groups: a struct with a
// field that holds a buffer is a group, and every field must then be
// a group member. Every struct is resolved before any is judged, since
// a group may hold a group declared after it, which only its own
// resolution records.
func (v *validator) collectGroups(pkg *Package) {
	scope := pkg.Types.Scope()
	var structs []*types.TypeName
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if _, ok := tn.Type().Underlying().(*types.Struct); ok {
			structs = append(structs, tn)
		}
	}
	for _, tn := range structs {
		v.resolveGroup(tn, map[*types.TypeName]bool{})
	}
	for _, tn := range structs {
		if v.groups[tn] {
			continue
		}
		st := tn.Type().Underlying().(*types.Struct)
		for i := 0; i < st.NumFields(); i++ {
			if v.mentionsBuffer(st.Field(i).Type()) {
				v.errs.add(tn.Pos(), "type %s holds a buffer, so it is a buffer group, and a group's fields are only variable-length buffers, fixed-size buffers or groups by pointer, and arrays of buffers", tn.Name())
				break
			}
		}
	}
}

// checkPackage validates one compute subpackage.
func (v *validator) checkPackage(pkg *Package) {
	v.pkg = pkg
	v.tables = map[*types.Var]bool{}
	v.params = map[*types.Var]bool{}
	v.calls = map[*types.Func][]*types.Func{}
	v.decl = map[*types.Func]token.Pos{}
	v.ptrWrites = map[*types.Var]token.Pos{}
	v.ptrFlow = map[*types.Var][]*types.Var{}
	v.aliasCalls = nil
	v.lenCalls = map[ast.Expr]bool{}

	v.collectGroups(pkg)
	for _, f := range pkg.Files {
		v.checkFileComments(f)
		for _, imp := range f.Imports {
			if path := strings.Trim(imp.Path.Value, `"`); path != v.prog.Parent.Path {
				v.errs.add(imp.Pos(), "compute package may import only %s", v.prog.Parent.Path)
			}
			// The parent's helpers are checked as qualified calls.
			if imp.Name != nil && imp.Name.Name == "." {
				v.errs.add(imp.Pos(), "a compute package imports %s by name", v.prog.Parent.Path)
			}
		}
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok {
				v.checkGenDecl(gd)
			}
		}
	}
	// Signatures first so the parameter set is complete before any
	// body or local is examined.
	decls := funcDecls(pkg.Files)
	for _, fd := range decls {
		v.checkSignature(fd)
	}
	for _, fd := range decls {
		v.checkBody(fd)
	}
	v.checkLocals()
	v.checkExprTypes()
	v.checkRecursion()
	v.collectPointerFlow(decls)
	v.checkEntries()
	v.checkParamsReadOnly()
	v.checkAliasing()
	v.checkCallOrder(decls)
}

// checkFileComments rejects build constraints and embed directives,
// which would make the embedded source differ from what go build
// compiled.
func (v *validator) checkFileComments(f *ast.File) {
	for _, g := range f.Comments {
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, "//go:build") || strings.HasPrefix(c.Text, "// +build") {
				v.errs.add(c.Pos(), "build constraints are outside the compute subset")
			}
			if strings.HasPrefix(c.Text, "//go:embed") {
				v.errs.add(c.Pos(), "embed directives are outside the compute subset")
			}
		}
	}
}

// checkGenDecl validates package-level type, const, and var
// declarations.
func (v *validator) checkGenDecl(gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if s.TypeParams != nil {
				v.errs.add(s.Pos(), "generic types are outside the compute subset")
				continue
			}
			obj := v.pkg.Info.Defs[s.Name].(*types.TypeName)
			if v.groups[obj] {
				continue
			}
			v.checkValueType(obj.Type(), s.Pos(), "type "+s.Name.Name)
		case *ast.ValueSpec:
			if gd.Tok == token.CONST {
				for _, n := range s.Names {
					if c, ok := v.pkg.Info.Defs[n].(*types.Const); ok && !isUntyped(c.Type()) {
						v.checkValueType(c.Type(), n.Pos(), "const "+n.Name)
					}
				}
				continue
			}
			for i, n := range s.Names {
				obj, ok := v.pkg.Info.Defs[n].(*types.Var)
				if !ok {
					continue
				}
				if !v.isScalarArray(obj.Type()) {
					v.errs.add(n.Pos(), "package-level var %s must be a lookup table (an array of scalars)", n.Name)
					continue
				}
				if i >= len(s.Values) {
					v.errs.add(n.Pos(), "lookup table %s has no initializer", n.Name)
					continue
				}
				if _, ok := s.Values[i].(*ast.CompositeLit); !ok {
					v.errs.add(s.Values[i].Pos(), "lookup table %s must be initialized by an array literal", n.Name)
					continue
				}
				v.tables[obj] = true
				v.expr(s.Values[i])
				// The lowering takes each element's folded value, so
				// every element is a constant.
				for _, el := range s.Values[i].(*ast.CompositeLit).Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						el = kv.Value
					}
					if v.pkg.Info.Types[el].Value == nil {
						v.errs.add(el.Pos(), "lookup table %s element must be a constant", n.Name)
					}
				}
			}
		}
	}
}

// checkSignature validates a function's receiver, parameters, and
// results, and records the parameter objects.
func (v *validator) checkSignature(fd *ast.FuncDecl) {
	if fd.Type.TypeParams != nil {
		v.errs.add(fd.Pos(), "generic functions are outside the compute subset")
		return
	}
	obj, ok := v.pkg.Info.Defs[fd.Name].(*types.Func)
	if !ok {
		return
	}
	v.decl[obj] = fd.Pos()
	if fd.Recv == nil && fd.Name.Name == "init" {
		v.errs.add(fd.Pos(), "init functions are outside the compute subset")
		return
	}
	sig := obj.Type().(*types.Signature)
	if sig.Variadic() {
		v.errs.add(fd.Pos(), "variadic functions are outside the compute subset")
	}
	if recv := sig.Recv(); recv != nil {
		v.params[recv] = true
		t := recv.Type()
		p, byPointer := t.(*types.Pointer)
		if byPointer {
			t = p.Elem()
		}
		named, ok := types.Unalias(t).(*types.Named)
		switch {
		case !ok || named.Obj().Pkg() != v.pkg.Types || !v.isStructOrScalar(named):
			v.errs.add(fd.Recv.Pos(), "method receiver must be a struct or scalar type of this package")
		case !byPointer && v.groupType(named):
			v.errs.add(fd.Recv.Pos(), "receiver: fixed-size buffers and buffer groups are passed by pointer")
		}
	}
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		v.params[p] = true
		v.checkParamType(p.Type(), p.Pos(), "parameter "+p.Name())
	}
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		if r.Name() != "" {
			v.params[r] = true
		}
		if _, ok := r.Type().(*types.Pointer); ok {
			v.errs.add(r.Pos(), "functions cannot return pointers")
			continue
		}
		v.checkValueType(r.Type(), fd.Type.Results.Pos(), "result")
	}
	if fd.Body == nil {
		v.errs.add(fd.Pos(), "function %s has no body", fd.Name.Name)
	}
}

// checkParamType allows what a parameter may be: a variable-length
// buffer by value, a fixed-size buffer or a buffer group by pointer, a
// pointer to a struct or scalar, or any value type.
func (v *validator) checkParamType(t types.Type, pos token.Pos, what string) {
	if p, ok := t.(*types.Pointer); ok {
		// isBuffer accepts a pointer to a buffer, so a pointer
		// target that is itself a pointer is refused first.
		if _, ok := types.Unalias(p.Elem()).(*types.Pointer); ok {
			v.errs.add(pos, "%s: pointers may refer only to a buffer, a struct, or a scalar", what)
			return
		}
		if v.isBuffer(p.Elem()) {
			if v.isSliceBufferType(p.Elem()) {
				v.errs.add(pos, "%s: a variable-length buffer is passed by value", what)
			}
			return
		}
		if named, ok := types.Unalias(p.Elem()).(*types.Named); ok && v.isStructOrScalar(named) {
			return
		}
		if _, ok := p.Elem().Underlying().(*types.Basic); ok {
			v.checkBasicKind(p.Elem(), pos)
			return
		}
		v.errs.add(pos, "%s: pointers may refer only to a buffer, a struct, or a scalar", what)
		return
	}
	if v.isBuffer(t) {
		if !v.isSliceBufferType(t) {
			v.errs.add(pos, "%s: fixed-size buffers and buffer groups are passed by pointer", what)
		}
		return
	}
	v.checkValueType(t, pos, what)
}

// checkBody validates a function body.
func (v *validator) checkBody(fd *ast.FuncDecl) {
	if fd.Body == nil || fd.Type.TypeParams != nil {
		return
	}
	obj, ok := v.pkg.Info.Defs[fd.Name].(*types.Func)
	if !ok {
		return
	}
	v.cur = obj
	v.phases = nil
	if v.isGroupEntry(obj) {
		// A barrier is defined only where every thread of the
		// workgroup executes it. The validator holds NextPhase to the
		// statements directly in the entry's body, outside any if,
		// switch, loop, or nested block, which is uniform control flow
		// by construction; a uniform loop bound would also be, but the
		// validator does not prove bounds uniform.
		v.phases = map[*ast.CallExpr]bool{}
		for _, s := range fd.Body.List {
			if es, ok := s.(*ast.ExprStmt); ok {
				if c, ok := es.X.(*ast.CallExpr); ok {
					v.phases[c] = true
				}
			}
		}
	}
	v.block(fd.Body, 0, false)
	v.cur, v.phases = nil, nil
}

// block walks a statement list. depth counts enclosing loops; inSwitch
// reports that the innermost breakable statement is a switch, where a
// break would leave the switch rather than the loop.
func (v *validator) block(b *ast.BlockStmt, depth int, inSwitch bool) {
	for _, s := range b.List {
		v.stmt(s, depth, inSwitch)
	}
}

func (v *validator) stmt(s ast.Stmt, depth int, inSwitch bool) {
	switch s := s.(type) {
	case nil, *ast.EmptyStmt:
	case *ast.BlockStmt:
		v.block(s, depth, inSwitch)
	case *ast.DeclStmt:
		gd := s.Decl.(*ast.GenDecl)
		if gd.Tok == token.TYPE {
			v.errs.add(s.Pos(), "local type declarations are outside the compute subset")
			return
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, e := range vs.Values {
					if len(vs.Values) == 1 && len(vs.Names) == 1 {
						v.value(e)
						continue
					}
					v.expr(e)
				}
			}
		}
	case *ast.ExprStmt:
		if t, ok := v.typeOf(s.X).(*types.Tuple); ok && t.Len() > 1 {
			v.errs.add(s.Pos(), "the results of a multi-result call must be assigned")
		}
		v.expr(s.X)
	case *ast.AssignStmt:
		if s.Tok == token.SHL_ASSIGN || s.Tok == token.SHR_ASSIGN {
			v.checkShift(s.Lhs[0], s.Rhs[0], s.Pos())
		}
		for _, l := range s.Lhs {
			if s.Tok == token.DEFINE {
				// New locals are checked with every other local. A
				// name the definition reuses is assigned.
				if id, ok := l.(*ast.Ident); ok && v.pkg.Info.Defs[id] != nil {
					continue
				}
			}
			v.target(l)
		}
		for _, r := range s.Rhs {
			if len(s.Lhs) == 1 && len(s.Rhs) == 1 && (s.Tok == token.DEFINE || s.Tok == token.ASSIGN) {
				v.value(r)
				continue
			}
			v.expr(r)
		}
	case *ast.IncDecStmt:
		v.target(s.X)
	case *ast.IfStmt:
		v.stmt(s.Init, depth, inSwitch)
		v.expr(s.Cond)
		v.block(s.Body, depth, inSwitch)
		v.stmt(s.Else, depth, inSwitch)
	case *ast.SwitchStmt:
		v.stmt(s.Init, depth, inSwitch)
		if s.Tag == nil {
			v.errs.add(s.Pos(), "switch must have an integer or bool tag")
		} else {
			v.expr(s.Tag)
			if b, ok := v.typeOf(s.Tag).Underlying().(*types.Basic); !ok || b.Info()&(types.IsInteger|types.IsBoolean) == 0 {
				v.errs.add(s.Tag.Pos(), "switch tag must be an integer or bool")
			}
		}
		for _, c := range s.Body.List {
			cc := c.(*ast.CaseClause)
			for _, e := range cc.List {
				if v.pkg.Info.Types[e].Value == nil {
					v.errs.add(e.Pos(), "switch cases must be constants")
				}
			}
			for _, st := range cc.Body {
				v.stmt(st, depth, true)
			}
		}
	case *ast.ForStmt:
		v.stmt(s.Init, depth, inSwitch)
		if s.Cond != nil {
			v.expr(s.Cond)
		}
		v.stmt(s.Post, depth, inSwitch)
		v.block(s.Body, depth+1, false)
	case *ast.BranchStmt:
		if s.Label != nil {
			v.errs.add(s.Pos(), "labeled branches are outside the compute subset")
			return
		}
		switch s.Tok {
		case token.BREAK:
			if depth == 0 {
				v.errs.add(s.Pos(), "break outside a loop")
			} else if inSwitch {
				v.errs.add(s.Pos(), "break inside a switch case leaves the switch, not the loop; restructure with a flag")
			}
		case token.CONTINUE:
			if depth == 0 {
				v.errs.add(s.Pos(), "continue outside a loop")
			}
		default:
			v.errs.add(s.Pos(), "%s is outside the compute subset", s.Tok)
		}
	case *ast.ReturnStmt:
		for _, e := range s.Results {
			v.expr(e)
		}
	case *ast.LabeledStmt:
		v.errs.add(s.Pos(), "labels are outside the compute subset")
	case *ast.RangeStmt:
		if b, ok := v.typeOf(s.X).Underlying().(*types.Basic); !ok || b.Info()&types.IsInteger == 0 {
			v.errs.add(s.Pos(), "range is allowed only over an integer")
			return
		}
		if s.Value != nil {
			v.errs.add(s.Value.Pos(), "range over an integer has no second variable")
		}
		v.expr(s.X)
		if s.Key != nil {
			if _, ok := s.Key.(*ast.Ident); !ok {
				v.errs.add(s.Key.Pos(), "a range key is a local")
			} else if s.Tok == token.ASSIGN {
				v.target(s.Key)
			}
		}
		v.block(s.Body, depth+1, false)
	case *ast.TypeSwitchStmt:
		v.errs.add(s.Pos(), "type switches are outside the compute subset")
	case *ast.DeferStmt, *ast.GoStmt, *ast.SelectStmt, *ast.SendStmt:
		v.errs.add(s.Pos(), "%T is outside the compute subset", s)
	default:
		v.errs.add(s.Pos(), "unsupported statement %T", s)
	}
}

// target validates the left side of an assignment: a local, a field or
// element of one, or a store through a pointer parameter. Tables and
// buffers are never assigned, and pointers are never stored.
func (v *validator) target(e ast.Expr) {
	switch t := e.(type) {
	case *ast.ParenExpr:
		v.target(t.X)
	case *ast.Ident:
		if t.Name == "_" {
			return
		}
		obj := v.objectOf(t)
		if vr, ok := obj.(*types.Var); ok {
			if v.tables[vr] {
				v.errs.add(t.Pos(), "lookup table %s is read-only", t.Name)
				return
			}
			if _, ok := vr.Type().(*types.Pointer); ok {
				v.errs.add(t.Pos(), "pointer %s cannot be reassigned", t.Name)
				return
			}
			if v.isBuffer(vr.Type()) {
				v.errs.add(t.Pos(), "buffers cannot be reassigned")
				return
			}
		}
	case *ast.SelectorExpr:
		if root := v.rootVar(t); root != nil && v.tables[root] {
			v.errs.add(t.Pos(), "lookup table is read-only")
			return
		}
		if v.mentionsBuffer(v.typeOf(t)) {
			v.errs.add(t.Pos(), "buffers cannot be reassigned")
			return
		}
		v.expr(t.X)
	case *ast.IndexExpr:
		if v.mentionsBuffer(v.typeOf(t.X)) {
			v.element(t, false)
			return
		}
		if root := v.rootVar(t); root != nil && v.tables[root] {
			v.errs.add(t.Pos(), "lookup table is read-only")
			return
		}
		v.expr(t.X)
		v.expr(t.Index)
	case *ast.StarExpr:
		if v.isBuffer(v.typeOf(t)) {
			v.errs.add(t.Pos(), "buffers cannot be reassigned")
			return
		}
		v.deref(t)
	default:
		v.errs.add(e.Pos(), "cannot assign to %T", e)
	}
}

// deref allows *p only where p is a pointer parameter.
func (v *validator) deref(s *ast.StarExpr) {
	if id, ok := ast.Unparen(s.X).(*ast.Ident); ok {
		if vr, ok := v.objectOf(id).(*types.Var); ok && v.params[vr] {
			if _, ok := vr.Type().(*types.Pointer); ok {
				return
			}
		}
	}
	v.errs.add(s.Pos(), "dereference is allowed only of a pointer parameter")
}

func (v *validator) expr(e ast.Expr) {
	switch e := e.(type) {
	case nil:
	case *ast.BasicLit:
	case *ast.Ident:
		switch obj := v.objectOf(e).(type) {
		case *types.Nil:
			v.errs.add(e.Pos(), "nil is outside the compute subset")
		case *types.Func:
			v.errs.add(e.Pos(), "function values are outside the compute subset")
		case *types.Builtin:
			v.errs.add(e.Pos(), "builtin %s is outside the compute subset", obj.Name())
		case *types.Var:
			// Indexing reads a table without visiting it here, and a
			// buffer's allowed uses go through bufferOperand.
			if v.tables[obj] {
				v.errs.add(e.Pos(), "lookup table %s is used only by indexing", e.Name)
			} else if v.mentionsBuffer(obj.Type()) {
				v.errs.add(e.Pos(), "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len")
			}
		}
	case *ast.ParenExpr:
		v.expr(e.X)
	case *ast.CompositeLit:
		t := v.typeOf(e)
		v.checkValueType(t, e.Pos(), "composite literal")
		_, isStruct := t.Underlying().(*types.Struct)
		for _, el := range e.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if !isStruct {
					v.expr(kv.Key)
				}
				v.expr(kv.Value)
				continue
			}
			v.expr(el)
		}
	case *ast.SelectorExpr:
		sel := v.pkg.Info.Selections[e]
		if sel == nil {
			// Qualified identifier into the parent package.
			if _, ok := v.pkg.Info.Uses[e.Sel].(*types.Func); ok {
				v.errs.add(e.Pos(), "function values are outside the compute subset")
			}
			return
		}
		if sel.Kind() != types.FieldVal {
			v.errs.add(e.Pos(), "method values are outside the compute subset")
			return
		}
		if v.mentionsBuffer(v.typeOf(e)) {
			v.errs.add(e.Pos(), "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len")
			return
		}
		v.expr(e.X)
	case *ast.IndexExpr:
		if v.mentionsBuffer(v.typeOf(e.X)) {
			v.element(e, false)
			return
		}
		table := false
		if id, ok := ast.Unparen(e.X).(*ast.Ident); ok {
			obj, _ := v.objectOf(id).(*types.Var)
			table = v.tables[obj]
		}
		if !table {
			v.expr(e.X)
		}
		v.expr(e.Index)
	case *ast.IndexListExpr:
		v.errs.add(e.Pos(), "generic instantiation is outside the compute subset")
	case *ast.SliceExpr:
		v.errs.add(e.Pos(), "slicing is outside the compute subset")
	case *ast.TypeAssertExpr:
		v.errs.add(e.Pos(), "type assertions are outside the compute subset")
	case *ast.FuncLit:
		v.errs.add(e.Pos(), "closures are outside the compute subset")
	case *ast.StarExpr:
		if v.mentionsBuffer(v.typeOf(e)) {
			v.errs.add(e.Pos(), "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len")
			return
		}
		v.deref(e)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.AND:
			v.errs.add(e.Pos(), "address-of is allowed only as a call argument")
		case token.ARROW:
			v.errs.add(e.Pos(), "channel receive is outside the compute subset")
		default:
			v.expr(e.X)
		}
	case *ast.BinaryExpr:
		v.expr(e.X)
		v.expr(e.Y)
		switch e.Op {
		case token.SHL, token.SHR:
			v.checkShift(e.X, e.Y, e.Pos())
		case token.EQL, token.NEQ:
			v.checkComparison(e)
		}
	case *ast.CallExpr:
		v.call(e)
	case *ast.KeyValueExpr:
		v.errs.add(e.Pos(), "unexpected key-value expression")
	default:
		// Type expressions are handled as conversion callees by call.
		if v.pkg.Info.Types[e].IsType() {
			v.errs.add(e.Pos(), "type used as an expression")
			return
		}
		v.errs.add(e.Pos(), "unsupported expression %T", e)
	}
}

// call validates a conversion, a len/cap of an array, or a call to a
// function or method of the compute tree, and records the call edge.
func (v *validator) call(c *ast.CallExpr) {
	if c.Ellipsis.IsValid() {
		v.errs.add(c.Ellipsis, "variadic calls are outside the compute subset")
	}
	if len(c.Args) == 1 {
		if t, ok := v.typeOf(c.Args[0]).(*types.Tuple); ok && t.Len() > 1 {
			v.errs.add(c.Pos(), "a multi-result call cannot be spread as the arguments of a call")
		}
	}
	fun := ast.Unparen(c.Fun)
	tv := v.pkg.Info.Types[fun]
	switch {
	case tv.IsType():
		if _, ok := tv.Type.Underlying().(*types.Basic); !ok {
			v.errs.add(c.Pos(), "conversions are allowed only to scalar types")
		} else {
			v.checkValueType(tv.Type, c.Pos(), "conversion")
		}
		if len(c.Args) == 1 && v.bufferLen(c.Args[0]) {
			if b, ok := tv.Type.Underlying().(*types.Basic); !ok || (b.Kind() != types.Int32 && b.Kind() != types.Uint32) {
				v.errs.add(c.Pos(), "the length of a buffer converts to int32 or uint32")
			}
			arg := ast.Unparen(c.Args[0]).(*ast.CallExpr)
			v.bufferOperand(arg.Args[0], false)
			v.lenCalls[arg] = true
			return
		}
	case tv.IsBuiltin():
		name := fun.(*ast.Ident).Name
		switch name {
		case "len", "cap":
			if len(c.Args) != 1 {
				return
			}
			arg := c.Args[0]
			if v.bufferLen(c) {
				v.errs.add(c.Pos(), "the length of a buffer is used as the operand of a conversion to int32 or uint32")
				return
			}
			if name == "len" && v.isBuffer(v.typeOf(arg)) && !v.groupType(v.typeOf(arg)) {
				v.bufferOperand(arg, false)
				return
			}
			if _, ok := v.typeOf(arg).Underlying().(*types.Array); !ok {
				v.errs.add(c.Pos(), "%s is allowed only on fixed-size arrays and on buffers (len)", name)
			}
			return
		case "min", "max":
			// The result type is the operands' type.
			if b, ok := v.typeOf(c).Underlying().(*types.Basic); !ok || (b.Kind() != types.Int32 && b.Kind() != types.Int64 && b.Kind() != types.Uint32) {
				v.errs.add(c.Pos(), "%s is allowed only on int32, int64, and uint32 operands", name)
			}
		default:
			v.errs.add(c.Pos(), "builtin %s is outside the compute subset", name)
			return
		}
	default:
		switch f := fun.(type) {
		case *ast.Ident:
			if fn, ok := v.objectOf(f).(*types.Func); ok {
				v.edge(fn)
				if v.isGroupEntry(fn) {
					v.errs.add(c.Pos(), "group entry point %s is not called; its NextPhase orders only its own dispatch", fn.Name())
				}
			} else {
				v.errs.add(c.Pos(), "call of a non-function")
			}
		case *ast.SelectorExpr:
			if sel := v.pkg.Info.Selections[f]; sel != nil {
				if sel.Kind() != types.MethodVal {
					v.errs.add(c.Pos(), "call of a field or method expression")
					break
				}
				fn := sel.Obj().(*types.Func)
				if fn.Pkg() != v.pkg.Types && fn.Pkg() != v.prog.Parent.Types {
					v.errs.add(c.Pos(), "call outside the compute tree")
				}
				v.edge(fn)
				_, ptrRecv := fn.Type().(*types.Signature).Recv().Type().(*types.Pointer)
				_, ptrX := v.typeOf(f.X).(*types.Pointer)
				switch {
				case v.mentionsBuffer(v.typeOf(f.X)):
					v.bufferOperand(f.X, false)
				case ptrRecv && !ptrX:
					// The call takes the receiver's address.
					v.address(f.X, f.X.Pos())
				default:
					v.expr(f.X)
				}
			} else if fn, ok := v.pkg.Info.Uses[f.Sel].(*types.Func); !ok || fn.Pkg() != v.prog.Parent.Types {
				v.errs.add(c.Pos(), "call outside the compute tree")
			} else if fn.Name() == "NextPhase" {
				if !v.isGroupEntry(v.cur) {
					v.errs.add(c.Pos(), "NextPhase is called only in the body of a group entry point")
				} else if !v.phases[c] {
					v.errs.add(c.Pos(), "NextPhase must be a statement directly in the group entry's body, outside any if, switch, or loop")
				}
			}
		default:
			v.errs.add(c.Pos(), "unsupported call target %T", fun)
		}
	}
	for _, a := range c.Args {
		if u, ok := ast.Unparen(a).(*ast.UnaryExpr); ok && u.Op == token.AND {
			v.address(u.X, u.Pos())
			continue
		}
		if v.mentionsBuffer(v.typeOf(a)) {
			v.bufferOperand(a, false)
			continue
		}
		v.expr(a)
	}
}

// bufferLen reports len of a variable-length buffer, whose Go type is
// int and whose value arrives with the binding.
func (v *validator) bufferLen(e ast.Expr) bool {
	c, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(c.Args) != 1 || v.pkg.Info.Types[c].Value != nil {
		return false
	}
	id, ok := ast.Unparen(c.Fun).(*ast.Ident)
	if !ok || id.Name != "len" {
		return false
	}
	if _, ok := v.objectOf(id).(*types.Builtin); !ok {
		return false
	}
	return v.isSliceBufferType(v.typeOf(c.Args[0]))
}

// value validates the whole value of a single assignment or
// definition: any expression, or the one place an element of an
// array of buffers may be chosen by a variable index, a word read
// from it.
func (v *validator) value(e ast.Expr) {
	if ix, ok := ast.Unparen(e).(*ast.IndexExpr); ok && v.mentionsBuffer(v.typeOf(ix.X)) {
		v.element(ix, true)
		return
	}
	v.expr(e)
}

// element validates a word read or written by indexing a word buffer:
// w[i], where w is a buffer operand and i an int32 or uint32 index. A
// byte buffer is read and written through its accessors, which give
// the address its width and byte order. choose allows the buffer to
// be an element of an array of buffers selected by a variable index.
func (v *validator) element(e *ast.IndexExpr, choose bool) {
	t := v.typeOf(e.X)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || !v.buffers[named.Obj()] || v.groups[named.Obj()] {
		v.errs.add(e.Pos(), "a buffer is used as the receiver of a helper, an argument, an indexed word buffer, or the operand of len")
		return
	}
	if !wordBuffer(named) {
		v.errs.add(e.Pos(), "byte buffer %s is read and written through its accessors", named.Obj().Name())
		return
	}
	v.bufferOperand(e.X, choose)
	v.expr(e.Index)
	if tv := v.pkg.Info.Types[e.Index]; tv.Value == nil {
		if b, ok := tv.Type.Underlying().(*types.Basic); !ok || (b.Kind() != types.Int32 && b.Kind() != types.Uint32) {
			v.errs.add(e.Index.Pos(), "a buffer index is an int32 or uint32")
		}
	}
}

// bufferOperand validates an expression that denotes a buffer or a
// buffer group: a parameter, or a path of field selections and
// constant array indices into a group parameter. choose allows the
// last step to index an array of buffers by a variable.
func (v *validator) bufferOperand(e ast.Expr, choose bool) {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		if vr, ok := v.objectOf(x).(*types.Var); ok && v.params[vr] {
			return
		}
	case *ast.SelectorExpr:
		if v.groupType(v.typeOf(x.X)) {
			v.bufferOperand(x.X, false)
			return
		}
	case *ast.IndexExpr:
		if _, ok := v.typeOf(x.X).Underlying().(*types.Array); ok && v.mentionsBuffer(v.typeOf(x.X)) {
			if v.pkg.Info.Types[x.Index].Value == nil {
				if !choose {
					v.errs.add(x.Pos(), "an array of buffers is indexed by a variable only to read a word, as the whole value of an assignment")
					return
				}
				v.expr(x.Index)
			}
			v.bufferOperand(x.X, false)
			return
		}
	}
	v.errs.add(e.Pos(), "a buffer is a parameter or a field of a buffer group parameter")
}

// address checks an operand whose address a call takes, an explicit
// &x argument or the receiver of a pointer-receiver method: a struct
// or scalar held in a local or parameter, or a field or element path
// into one. The operand's root is a local or a parameter (a pointer
// parameter's target is a thread-local value on every backend), never
// a table or a buffer, and the operand itself is not a pointer.
func (v *validator) address(e ast.Expr, pos token.Pos) {
	x := ast.Unparen(e)
	root := v.rootVar(x)
	if root != nil && !v.tables[root] && root.Parent() != v.pkg.Types.Scope() && !v.isBuffer(root.Type()) {
		if _, isPtr := v.typeOf(x).(*types.Pointer); !isPtr && !v.isBuffer(v.typeOf(x)) {
			v.expr(x)
			return
		}
	}
	v.errs.add(pos, "address-of is allowed only of a struct or scalar held in a local or parameter")
}

// isGroupEntry reports whether fn is one of the package's entry
// points in the group form: three trailing integer coordinates.
func (v *validator) isGroupEntry(fn *types.Func) bool {
	if fn == nil {
		return false
	}
	entry := false
	for _, name := range v.pkg.Entries {
		entry = entry || v.pkg.Types.Scope().Lookup(name) == types.Object(fn)
	}
	if !entry {
		return false
	}
	sig := fn.Type().(*types.Signature)
	coords := 0
	for i := 0; i < sig.Params().Len(); i++ {
		if _, ok := sig.Params().At(i).Type().Underlying().(*types.Basic); ok {
			coords++
		}
	}
	return coords == 3
}

// edge records that the current function calls fn, for the recursion
// check. Calls into the parent's helpers cannot recurse.
func (v *validator) edge(fn *types.Func) {
	if v.cur == nil || fn.Pkg() != v.pkg.Types {
		return
	}
	v.calls[v.cur] = append(v.calls[v.cur], fn)
}

// checkShift rejects a constant shift count that reaches the width of
// the shifted operand's declared type, for a shift expression and for
// a compound shift assignment.
func (v *validator) checkShift(x, count ast.Expr, pos token.Pos) {
	cv := v.pkg.Info.Types[count].Value
	if cv == nil {
		return
	}
	b, ok := v.typeOf(x).Underlying().(*types.Basic)
	if !ok || isUntyped(b) {
		return
	}
	width := basicWidth(b)
	if width == 0 {
		return
	}
	if n, ok := constant.Uint64Val(constant.ToInt(cv)); ok && n >= uint64(width) {
		v.errs.add(pos, "shift count %d reaches the %d-bit width of the operand", n, width)
	}
}

// checkComparison rejects == and != on an operand that is not a
// scalar: Go compares structs, arrays, and pointers, the targets do
// not.
func (v *validator) checkComparison(e *ast.BinaryExpr) {
	for _, x := range []ast.Expr{e.X, e.Y} {
		t := v.typeOf(x)
		if _, ok := t.Underlying().(*types.Basic); !ok {
			v.errs.add(e.Pos(), "comparison of non-scalar type %s is outside the compute subset", t)
			return
		}
	}
}

// checkLocals verifies the type of every local variable. Parameters
// were checked with their signatures; locals may not hold pointers or
// buffers.
func (v *validator) checkLocals() {
	for id, obj := range v.pkg.Info.Defs {
		vr, ok := obj.(*types.Var)
		if !ok || vr.IsField() || v.params[vr] || vr.Parent() == v.pkg.Types.Scope() {
			continue
		}
		if _, ok := types.Unalias(vr.Type()).(*types.Pointer); ok {
			v.errs.add(id.Pos(), "local %s: pointers cannot be stored in locals", id.Name)
			continue
		}
		v.checkValueType(vr.Type(), id.Pos(), "local "+id.Name)
	}
}

// checkExprTypes rejects any expression whose type is outside the
// subset's scalar set, which catches implementation-sized int arising
// from untyped constants, and any constant whose type has no lowering.
// A buffer's length held to a conversion's operand is an int the
// conversion takes, parenthesized or not.
func (v *validator) checkExprTypes() {
	for e, tv := range v.pkg.Info.Types {
		if !tv.IsValue() || v.lenCalls[ast.Unparen(e)] {
			continue
		}
		if tv.Value != nil {
			v.checkConstKind(tv.Type, e.Pos())
			continue
		}
		if tuple, ok := tv.Type.(*types.Tuple); ok {
			for i := 0; i < tuple.Len(); i++ {
				v.checkBasicKind(tuple.At(i).Type(), e.Pos())
			}
			continue
		}
		v.checkBasicKind(tv.Type, e.Pos())
	}
}

// checkRecursion reports call cycles among the package's functions.
func (v *validator) checkRecursion() {
	const (
		white = iota
		grey
		black
	)
	color := map[*types.Func]int{}
	var stack []*types.Func
	var visit func(f *types.Func)
	visit = func(f *types.Func) {
		color[f] = grey
		stack = append(stack, f)
		for _, callee := range v.calls[f] {
			switch color[callee] {
			case white:
				visit(callee)
			case grey:
				var names []string
				for i := len(stack) - 1; i >= 0; i-- {
					names = append([]string{stack[i].Name()}, names...)
					if stack[i] == callee {
						break
					}
				}
				v.errs.add(v.decl[f], "recursive call chain: %s -> %s", strings.Join(names, " -> "), callee.Name())
			}
		}
		stack = stack[:len(stack)-1]
		color[f] = black
	}
	fns := make([]*types.Func, 0, len(v.decl))
	for f := range v.decl {
		fns = append(fns, f)
	}
	sort.Slice(fns, func(i, j int) bool { return v.decl[fns[i]] < v.decl[fns[j]] })
	for _, f := range fns {
		if color[f] == white {
			visit(f)
		}
	}
}

// checkEntries verifies each named entry point exists, is exported, has
// no results, and takes buffers, then at most two structs by pointer,
// then one to three integer coordinates: one for an index entry, two
// for a span entry, three for a group entry. The struct parameters
// are buffer-visible and must hold only bools and 32-bit and 64-bit
// scalars.
//
// The two struct parameters are not interchangeable. The first is the
// params row, which a dispatch loads from the params buffer; the
// second is row state, which every renderer starts at its zero value
// and the entry carries across the software renderer's span calls.
// checkParamsReadOnly holds the order by the one difference the
// source shows: the params row is never written through.
func (v *validator) checkEntries() {
	for _, name := range v.pkg.Entries {
		obj, ok := v.pkg.Types.Scope().Lookup(name).(*types.Func)
		if !ok || !obj.Exported() {
			v.errs.add(token.NoPos, "%s: entry point %s is not an exported function", v.pkg.Path, name)
			continue
		}
		sig := obj.Type().(*types.Signature)
		if sig.Results().Len() != 0 {
			v.errs.add(obj.Pos(), "entry point %s must not return values", name)
		}
		stage, structs, coords := 0, 0, 0
		for i := 0; i < sig.Params().Len(); i++ {
			p := sig.Params().At(i)
			t := p.Type()
			ptr, isPtr := t.(*types.Pointer)
			switch {
			case isPtr && v.isBuffer(ptr.Elem()), !isPtr && v.isBuffer(t):
				if stage > 0 {
					v.errs.add(p.Pos(), "entry point %s: buffers must precede the params struct", name)
				}
			case isPtr:
				if _, ok := ptr.Elem().Underlying().(*types.Struct); !ok {
					v.errs.add(p.Pos(), "entry point %s: parameter %s must be a buffer, a struct pointer, or an int32/uint32 coordinate", name, p.Name())
					continue
				}
				if stage > 1 || structs == 2 {
					v.errs.add(p.Pos(), "entry point %s: at most two structs by pointer, after the buffers", name)
				}
				stage, structs = 1, structs+1
				v.checkBufferVisible(ptr.Elem(), p.Pos(), "entry point "+name+" parameter "+p.Name())
			default:
				b, ok := t.Underlying().(*types.Basic)
				if !ok || (b.Kind() != types.Int32 && b.Kind() != types.Uint32) {
					v.errs.add(p.Pos(), "entry point %s: parameter %s must be a buffer, a struct pointer, or an int32/uint32 coordinate", name, p.Name())
					continue
				}
				stage, coords = 2, coords+1
			}
		}
		if coords == 0 || coords > 3 {
			v.errs.add(obj.Pos(), "entry point %s must end with one to three int32/uint32 coordinates", name)
		}
	}
}

// collectPointerFlow records, over every function of the package,
// where a pointer parameter is written through and which callee
// parameter a pointer parameter reaches when it is passed on, whole
// (p) or as a path into it (&p.f[i] as an argument, p.f as the
// receiver of a pointer method). Since the subset forbids storing a
// pointer in a local, a struct, or an array, and forbids reassigning
// one, a pointer moves only as a call's argument or receiver, so
// these two facts are the whole of what a pointer parameter can
// reach.
func (v *validator) collectPointerFlow(decls []*ast.FuncDecl) {
	for _, fd := range decls {
		if fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range s.Lhs {
					v.notePointerWrite(lhs)
				}
			case *ast.IncDecStmt:
				v.notePointerWrite(s.X)
			case *ast.CallExpr:
				v.notePointerArgs(s)
				v.noteRefArgs(s)
			}
			return true
		})
	}
}

// callee is the function or method a call names, or nil for a
// builtin, a conversion, or a call of a value.
func (v *validator) callee(c *ast.CallExpr) *types.Func {
	switch f := ast.Unparen(c.Fun).(type) {
	case *ast.Ident:
		fn, _ := v.objectOf(f).(*types.Func)
		return fn
	case *ast.SelectorExpr:
		if sel := v.pkg.Info.Selections[f]; sel != nil {
			fn, _ := sel.Obj().(*types.Func)
			return fn
		}
		fn, _ := v.objectOf(f.Sel).(*types.Func)
		return fn
	}
	return nil
}

// callArgs visits a call's receiver, at index 0, and each argument,
// at its position plus one, with the parameter it lands in.
func callArgs(c *ast.CallExpr, fn *types.Func, visit func(index int, param *types.Var, arg ast.Expr)) {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return
	}
	if recv := sig.Recv(); recv != nil {
		if sel, ok := ast.Unparen(c.Fun).(*ast.SelectorExpr); ok {
			visit(0, recv, sel.X)
		}
	}
	for i, arg := range c.Args {
		if i >= sig.Params().Len() {
			break
		}
		visit(i+1, sig.Params().At(i), arg)
	}
}

// pointerParam is the pointer parameter an expression is rooted in,
// or nil: the parameter itself, a path into it (p.f[i], *p), or the
// address of one (&p.f).
func (v *validator) pointerParam(e ast.Expr) *types.Var {
	root, _ := v.refPath(e)
	if root == nil || !v.params[root] {
		return nil
	}
	if _, ok := root.Type().(*types.Pointer); !ok {
		return nil
	}
	return root
}

// notePointerWrite records an assignment target rooted in a pointer
// parameter, the only way a function writes through one.
func (v *validator) notePointerWrite(e ast.Expr) {
	if vr := v.pointerParam(e); vr != nil {
		if _, seen := v.ptrWrites[vr]; !seen {
			v.ptrWrites[vr] = e.Pos()
		}
	}
}

// notePointerArgs records each pointer parameter a call passes on, as
// the receiver or as an argument, whole or as a path into it, to the
// pointer parameter of the callee it lands in. Only calls within the
// package matter: the parent's helpers take buffers and scalars.
func (v *validator) notePointerArgs(c *ast.CallExpr) {
	fn := v.callee(c)
	if fn == nil || fn.Pkg() != v.pkg.Types {
		return
	}
	callArgs(c, fn, func(_ int, to *types.Var, arg ast.Expr) {
		if _, ok := to.Type().(*types.Pointer); !ok {
			return
		}
		if from := v.pointerParam(arg); from != nil {
			v.ptrFlow[from] = append(v.ptrFlow[from], to)
		}
	})
}

// checkParamsReadOnly holds every entry point's params row read-only.
// A dispatch reads the row from the params buffer, so a write through
// it reaches the caller's struct in the software renderer and not the
// params row on a device: the same compute function would then draw
// two different things. A struct an entry updates is its second
// struct pointer, which every renderer starts at its zero value, so
// the rule is also what keeps the two from being written in the other
// order.
//
// The flow is by parameter, not by call site, so a helper that takes
// a params row from one entry and a row state from another is
// reported for the write it makes on behalf of the second. Splitting
// it in two is the answer: one of them writes and one does not.
func (v *validator) checkParamsReadOnly() {
	for _, name := range v.pkg.Entries {
		obj, ok := v.pkg.Types.Scope().Lookup(name).(*types.Func)
		if !ok {
			continue
		}
		sig, ok := obj.Type().(*types.Signature)
		if !ok {
			continue
		}
		row := v.paramsRow(sig)
		if row == nil {
			continue
		}
		seen := map[*types.Var]bool{row: true}
		for queue := []*types.Var{row}; len(queue) > 0; {
			cur := queue[0]
			queue = queue[1:]
			if pos, ok := v.ptrWrites[cur]; ok {
				v.errs.add(pos, "writes through %s, the params row of entry point %s, which is read-only; a struct an entry updates must be its second struct pointer", cur.Name(), name)
				break
			}
			for _, next := range v.ptrFlow[cur] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
}

// paramsRow is an entry point's first struct pointer, the params row,
// or nil when it has none.
func (v *validator) paramsRow(sig *types.Signature) *types.Var {
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		ptr, ok := p.Type().(*types.Pointer)
		if ok && !v.isBuffer(ptr.Elem()) {
			return p
		}
	}
	return nil
}

// checkBufferVisible verifies a struct that crosses the buffer boundary
// holds only bools and 32-bit and 64-bit scalars, arrays of them, and
// nested structs of the same. A bool is the byte Go stores, which a
// backend reads as the word holding it and tests against zero.
func (v *validator) checkBufferVisible(t types.Type, pos token.Pos, what string) {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Bool, types.Int32, types.Uint32, types.Int64, types.Uint64, types.Float32:
			return
		}
		v.errs.add(pos, "%s: buffer-visible field type %s must be a bool or a 32-bit or 64-bit scalar", what, t)
	case *types.Array:
		v.checkBufferVisible(u.Elem(), pos, what)
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			v.checkBufferVisible(f.Type(), f.Pos(), what+" field "+f.Name())
		}
	default:
		v.errs.add(pos, "%s: %s is not a buffer-visible type", what, t)
	}
}

// checkValueType verifies a value type: an allowed scalar, a non-empty
// array of value types, or a struct of value types without embedding.
func (v *validator) checkValueType(t types.Type, pos token.Pos, what string) {
	t = types.Unalias(t)
	if v.isBuffer(t) {
		v.errs.add(pos, "%s: buffers appear only as parameters", what)
		return
	}
	if _, ok := t.(*types.Struct); ok {
		// The lowering numbers structs by their declarations.
		v.errs.add(pos, "%s: an unnamed struct type is outside the compute subset", what)
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		v.checkBasicKind(u, pos)
	case *types.Array:
		if u.Len() == 0 {
			v.errs.add(pos, "%s: zero-length arrays are outside the compute subset", what)
			return
		}
		v.checkValueType(u.Elem(), pos, what)
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			if f.Embedded() {
				v.errs.add(f.Pos(), "%s: embedded fields are outside the compute subset", what)
				continue
			}
			if _, ok := f.Type().(*types.Pointer); ok {
				v.errs.add(f.Pos(), "%s: pointers cannot be stored in structs", what)
				continue
			}
			v.checkValueType(f.Type(), f.Pos(), what+" field "+f.Name())
		}
	case *types.Pointer:
		v.errs.add(pos, "%s: pointer type outside a parameter", what)
	default:
		v.errs.add(pos, "%s: %s is outside the compute subset", what, t)
	}
}

// checkConstKind rejects a constant whose type is outside the subset.
// The implementation-sized and the untyped integer, rune, bool, and
// float kinds appear as constants (indices, shift counts, literals)
// and lower to the subset's scalars. A string or a 64-bit float
// constant has no lowering.
func (v *validator) checkConstKind(t types.Type, pos token.Pos) {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return
	}
	switch b.Kind() {
	case types.Bool, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Float32,
		types.Int, types.Uint, types.UntypedBool, types.UntypedInt, types.UntypedRune, types.UntypedFloat,
		types.Invalid:
		return
	}
	v.errs.add(pos, "constant of type %s is outside the compute subset", t)
}

// checkBasicKind rejects the basic types outside the subset.
func (v *validator) checkBasicKind(t types.Type, pos token.Pos) {
	b, ok := t.Underlying().(*types.Basic)
	if !ok || isUntyped(b) {
		return
	}
	switch b.Kind() {
	case types.Bool, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Float32:
		return
	case types.Invalid:
		return
	}
	v.errs.add(pos, "type %s is outside the compute subset", t)
}

// isBuffer reports a buffer or a buffer group, or a pointer to one.
func (v *validator) isBuffer(t types.Type) bool {
	named := namedOf(t)
	return named != nil && v.buffers[named.Obj()]
}

// groupType reports a buffer group or a pointer to one.
func (v *validator) groupType(t types.Type) bool {
	named := namedOf(t)
	return named != nil && v.groups[named.Obj()]
}

// isFixedBuffer reports a fixed-size buffer type.
func (v *validator) isFixedBuffer(named *types.Named) bool {
	_, ok := named.Underlying().(*types.Array)
	return ok && v.buffers[named.Obj()]
}

// isSliceBufferType reports a variable-length buffer type.
func (v *validator) isSliceBufferType(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	_, slice := named.Underlying().(*types.Slice)
	return slice && v.buffers[named.Obj()]
}

// wordBuffer reports a buffer type of 32-bit words.
func wordBuffer(named *types.Named) bool {
	var elem types.Type
	switch u := named.Underlying().(type) {
	case *types.Array:
		elem = u.Elem()
	case *types.Slice:
		elem = u.Elem()
	default:
		return false
	}
	b, ok := elem.Underlying().(*types.Basic)
	return ok && b.Kind() == types.Uint32
}

func (v *validator) isStructOrScalar(named *types.Named) bool {
	switch named.Underlying().(type) {
	case *types.Struct, *types.Basic:
		return true
	}
	return false
}

func (v *validator) isScalarArray(t types.Type) bool {
	arr, ok := t.Underlying().(*types.Array)
	if !ok {
		return false
	}
	_, ok = arr.Elem().Underlying().(*types.Basic)
	return ok
}

func (v *validator) typeOf(e ast.Expr) types.Type {
	if t := v.pkg.Info.Types[e].Type; t != nil {
		return t
	}
	return types.Typ[types.Invalid]
}

func (v *validator) objectOf(id *ast.Ident) types.Object {
	if obj := v.pkg.Info.Uses[id]; obj != nil {
		return obj
	}
	return v.pkg.Info.Defs[id]
}

// rootVar returns the variable at the base of a selector or index
// chain, or nil.
func (v *validator) rootVar(e ast.Expr) *types.Var {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.Ident:
			vr, _ := v.objectOf(x).(*types.Var)
			return vr
		default:
			return nil
		}
	}
}

func isUntyped(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsUntyped != 0
}

func basicWidth(b *types.Basic) int {
	switch b.Kind() {
	case types.Int8, types.Uint8:
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32, types.Float32:
		return 32
	case types.Int64, types.Uint64:
		return 64
	}
	return 0
}
