// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/token"
	"go/types"
)

// Go leaves unspecified the order of a call and a plain read in one
// statement. The gc compiler, which builds the software renderer, runs
// every call of a statement before its plain reads, word reads w[i]
// included. The lowering reads a plain operand where it stands and
// orders a word read with the calls, so the two differ when a call
// writes what the statement reads outside it. The subset rejects that
// statement.

// access is storage a statement reads or a call may write: a path
// from a variable, or the path to a buffer, which a call takes whole.
type access struct {
	root   *types.Var
	path   []pathStep
	buffer bool
}

// checkCallOrder rejects every statement in which a call may write
// storage the statement reads outside that call.
func (v *validator) checkCallOrder(decls []*ast.FuncDecl) {
	for _, fd := range decls {
		if fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				v.statementOrder(s.Lhs, s.Tok != token.ASSIGN && s.Tok != token.DEFINE, s.Rhs)
			case *ast.IncDecStmt:
				v.statementOrder([]ast.Expr{s.X}, true, nil)
			case *ast.ExprStmt:
				v.statementOrder(nil, false, []ast.Expr{s.X})
			case *ast.ReturnStmt:
				v.statementOrder(nil, false, s.Results)
			case *ast.ValueSpec:
				v.statementOrder(nil, false, s.Values)
			case *ast.IfStmt:
				v.statementOrder(nil, false, []ast.Expr{s.Cond})
			case *ast.ForStmt:
				if s.Cond != nil {
					v.statementOrder(nil, false, []ast.Expr{s.Cond})
				}
			case *ast.SwitchStmt:
				if s.Tag != nil {
					v.statementOrder(nil, false, []ast.Expr{s.Tag})
				}
			case *ast.RangeStmt:
				v.statementOrder(nil, false, []ast.Expr{s.X})
			}
			return true
		})
	}
}

// statementOrder checks one statement: its targets, read whole when
// compound reads them, and its values.
func (v *validator) statementOrder(targets []ast.Expr, compound bool, values []ast.Expr) {
	var calls []*ast.CallExpr
	for _, list := range [][]ast.Expr{targets, values} {
		for _, e := range list {
			ast.Inspect(e, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					calls = append(calls, c)
				}
				return true
			})
		}
	}
	for _, c := range calls {
		fn := v.callee(c)
		if fn == nil {
			continue
		}
		writes := v.callWrites(c, fn)
		if len(writes) == 0 {
			continue
		}
		type rootKind struct {
			root   *types.Var
			buffer bool
		}
		reported := map[rootKind]bool{}
		check := func(r access) {
			for _, w := range writes {
				key := rootKind{w.root, w.buffer}
				if r.root != w.root || r.buffer != w.buffer || !overlap(r.path, w.path) || reported[key] {
					continue
				}
				reported[key] = true
				if w.buffer {
					v.errs.add(c.Pos(), "the statement indexes buffer %s outside its call to %s, which takes it; the software renderer runs the call first and a device may read first", w.root.Name(), fn.Name())
				} else {
					v.errs.add(c.Pos(), "the statement reads %s outside its call to %s, which writes it; the software renderer runs the call first and a device may read first", w.root.Name(), fn.Name())
				}
			}
		}
		for _, t := range targets {
			if compound {
				v.reads(t, c, check)
			} else {
				v.indexReads(t, c, check)
			}
		}
		for _, e := range values {
			v.reads(e, c, check)
		}
	}
}

// callWrites is the storage a call may write: every buffer it takes,
// as the receiver or an argument, and the target of each reference
// argument whose parameter the callee writes through.
func (v *validator) callWrites(c *ast.CallExpr, fn *types.Func) []access {
	var out []access
	callArgs(c, fn, func(_ int, param *types.Var, arg ast.Expr) {
		if v.isBuffer(v.typeOf(arg)) {
			if root, path := v.refPath(arg); root != nil {
				out = append(out, access{root: root, path: path, buffer: true})
			}
			return
		}
		if _, ok := param.Type().(*types.Pointer); !ok || fn.Pkg() != v.pkg.Types {
			return
		}
		if !v.writesThrough(param, map[*types.Var]bool{}) {
			return
		}
		if root, path := v.refPath(arg); root != nil {
			out = append(out, access{root: root, path: path})
		}
	})
	return out
}

// reads reports the storage an expression reads outside the call
// skip: each variable path it reads and each buffer it indexes. A
// reference or a buffer passed to a call is not a read, since the
// call it reaches is ordered with the other calls.
func (v *validator) reads(e ast.Expr, skip *ast.CallExpr, add func(access)) {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		if vr, ok := v.objectOf(x).(*types.Var); ok && v.heldValue(vr) {
			add(access{root: vr})
		}
	case *ast.SelectorExpr, *ast.StarExpr:
		v.pathRead(x, skip, add)
	case *ast.IndexExpr:
		if v.isBuffer(v.typeOf(x.X)) {
			if root, path := v.refPath(x.X); root != nil {
				add(access{root: root, path: path, buffer: true})
			}
			v.indexReads(x.X, skip, add)
			v.reads(x.Index, skip, add)
			return
		}
		v.pathRead(x, skip, add)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			v.indexReads(x.X, skip, add)
			return
		}
		v.reads(x.X, skip, add)
	case *ast.BinaryExpr:
		v.reads(x.X, skip, add)
		v.reads(x.Y, skip, add)
	case *ast.CallExpr:
		if x == skip {
			return
		}
		if fn := v.callee(x); fn != nil {
			if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
				sel := ast.Unparen(x.Fun).(*ast.SelectorExpr)
				if _, ok := recv.Type().(*types.Pointer); ok {
					v.indexReads(sel.X, skip, add)
				} else {
					v.reads(sel.X, skip, add)
				}
			}
		}
		for _, a := range x.Args {
			v.reads(a, skip, add)
		}
	case *ast.CompositeLit:
		for _, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				el = kv.Value
			}
			v.reads(el, skip, add)
		}
	}
}

// heldValue reports a variable a read takes the value of: not a
// pointer, a buffer, or a lookup table, which no call writes.
func (v *validator) heldValue(vr *types.Var) bool {
	_, ptr := vr.Type().(*types.Pointer)
	return !ptr && !v.isBuffer(vr.Type()) && !v.tables[vr]
}

// pathRead reports a field or element path as a read of its root,
// unless it names a buffer, and reads its index operands.
func (v *validator) pathRead(e ast.Expr, skip *ast.CallExpr, add func(access)) {
	if !v.isBuffer(v.typeOf(e)) {
		if root, path := v.refPath(e); root != nil && !v.tables[root] {
			add(access{root: root, path: path})
		}
	}
	v.indexReads(e, skip, add)
}

// indexReads reports the reads of a path's index operands and of the
// expression it starts from when that is not a variable.
func (v *validator) indexReads(e ast.Expr, skip *ast.CallExpr, add func(access)) {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.SelectorExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			v.reads(x.Index, skip, add)
			e = x.X
		case *ast.Ident:
			return
		default:
			v.reads(x, skip, add)
			return
		}
	}
}
