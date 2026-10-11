// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// A reference argument may be a copy on a device: the emitters pass Go
// pointers as references, and a target may implement a reference by
// copying the value in at the call and back at the return, so two
// reference arguments of one call that overlap the same storage behave
// as two private copies there and as one object in Go. The difference shows
// only when the callee writes through one of them, so that is the
// rule: a call may pass overlapping references only to a callee
// that writes through neither.

// refArg is one reference argument of a call: the callee parameter
// it lands in, the variable it is rooted in, and its access path
// from that root.
type refArg struct {
	index int
	param *types.Var
	root  *types.Var
	path  []pathStep
}

// pathStep is one field selection or one array index on the way from
// a root variable to the referenced storage. An index that is not a
// constant may be any element.
type pathStep struct {
	field   string
	index   int64
	isIndex bool
	dynamic bool
}

// aliasCall is a call with the reference arguments it passes.
type aliasCall struct {
	pos  token.Pos
	fn   *types.Func
	refs []refArg
}

// noteRefArgs records a call's reference arguments: the receiver of
// a pointer-receiver method and every argument whose parameter is a
// pointer to a struct or scalar. Buffers are excluded: a buffer is
// the same device memory however many times it is bound.
func (v *validator) noteRefArgs(c *ast.CallExpr) {
	fn := v.callee(c)
	if fn == nil || fn.Pkg() != v.pkg.Types {
		return
	}
	call := aliasCall{pos: c.Pos(), fn: fn}
	callArgs(c, fn, func(index int, param *types.Var, arg ast.Expr) {
		ptr, ok := param.Type().(*types.Pointer)
		if !ok || v.isBuffer(ptr.Elem()) {
			return
		}
		root, path := v.refPath(arg)
		if root != nil {
			call.refs = append(call.refs, refArg{index: index, param: param, root: root, path: path})
		}
	})
	if len(call.refs) > 1 {
		v.aliasCalls = append(v.aliasCalls, call)
	}
}

// refPath resolves a reference argument to the variable it is rooted
// in and the path of fields and indices from there: &x.f[i] is x
// with the path f, i; a pointer parameter p passed on is p with an
// empty path; &p.f is p with the path f. An expression that is not a
// path into a variable is no reference.
func (v *validator) refPath(e ast.Expr) (*types.Var, []pathStep) {
	e = ast.Unparen(e)
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = ast.Unparen(u.X)
	}
	var steps []pathStep
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.SelectorExpr:
			steps = append(steps, pathStep{field: x.Sel.Name})
			e = x.X
		case *ast.IndexExpr:
			step := pathStep{isIndex: true, dynamic: true}
			if tv := v.pkg.Info.Types[x.Index]; tv.Value != nil {
				if n, ok := constant.Int64Val(constant.ToInt(tv.Value)); ok {
					step.index, step.dynamic = n, false
				}
			}
			steps = append(steps, step)
			e = x.X
		case *ast.Ident:
			vr, _ := v.objectOf(x).(*types.Var)
			if vr == nil {
				return nil, nil
			}
			for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
				steps[i], steps[j] = steps[j], steps[i]
			}
			return vr, steps
		default:
			return nil, nil
		}
	}
}

// overlap reports whether two paths from one root can reach the same
// storage: one is a prefix of the other, with fields matching by name
// and indices matching unless both are constants that differ.
func overlap(a, b []pathStep) bool {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		x, y := a[i], b[i]
		if x.isIndex != y.isIndex {
			return false
		}
		if x.isIndex {
			if !x.dynamic && !y.dynamic && x.index != y.index {
				return false
			}
			continue
		}
		if x.field != y.field {
			return false
		}
	}
	return true
}

// writesThrough reports whether a function writes through the
// pointer parameter, itself or through a callee it passes the
// pointer to. The call graph is acyclic, so the walk ends.
func (v *validator) writesThrough(p *types.Var, seen map[*types.Var]bool) bool {
	if _, ok := v.ptrWrites[p]; ok {
		return true
	}
	if seen[p] {
		return false
	}
	seen[p] = true
	for _, next := range v.ptrFlow[p] {
		if v.writesThrough(next, seen) {
			return true
		}
	}
	return false
}

// checkAliasing rejects every call that passes two overlapping
// references to a callee that writes through one of them.
func (v *validator) checkAliasing() {
	for _, c := range v.aliasCalls {
		for i := 0; i < len(c.refs); i++ {
			for j := i + 1; j < len(c.refs); j++ {
				a, b := c.refs[i], c.refs[j]
				if a.root != b.root || !overlap(a.path, b.path) {
					continue
				}
				if !v.writesThrough(a.param, map[*types.Var]bool{}) && !v.writesThrough(b.param, map[*types.Var]bool{}) {
					continue
				}
				v.errs.add(c.pos, "arguments %d and %d of the call to %s reference overlapping storage of %s, and %s writes through one of them; a reference is a copy on a device, so the two would not see each other's writes",
					a.index, b.index, c.fn.Name(), a.root.Name(), c.fn.Name())
			}
		}
	}
}
