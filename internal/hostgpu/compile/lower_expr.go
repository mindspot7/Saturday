// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// expr lowers an expression. Constants fold to Const, an
// implementation-sized or untyped integer one only when its value fits
// the 32 bits it lowers to (narrowed); a short-circuit operator whose
// right operand calls is rewritten into statements emitted ahead of
// the expression.
func (lw *lowerer) expr(e ast.Expr) Expr {
	tv := lw.pkg.Info.Types[e]
	if tv.Value != nil {
		if narrowed(tv.Type, tv.Value) {
			lw.diag.add(e.Pos(), "constant %s does not fit the 32-bit integer %s lowers to", tv.Value, tv.Type)
		}
		return &Const{T: lw.typ(tv.Type), Value: tv.Value}
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return lw.expr(e.X)
	case *ast.Ident:
		li, ok := lw.localOf(e)
		if !ok {
			return lw.unexpected(e.Pos(), "unresolved identifier %s", e.Name)
		}
		return &LocalRef{T: lw.fn.Locals[li].Type, Index: li}
	case *ast.SelectorExpr:
		return lw.selector(e)
	case *ast.IndexExpr:
		if lw.wordElement(e) {
			return lw.load(e)
		}
		if id, ok := ast.Unparen(e.X).(*ast.Ident); ok {
			if obj, ok := lw.pkg.Info.Uses[id].(*types.Var); ok {
				if ti, ok := lw.tables[obj]; ok {
					return &TableRef{T: lw.typeOf(e), Table: ti, Index: lw.canon(lw.expr(e.Index))}
				}
			}
		}
		xs := []Expr{lw.expr(e.X)}
		m := lw.mark()
		idx := lw.canon(lw.expr(e.Index))
		lw.keepOrder(m, xs)
		return &IndexRef{T: lw.typeOf(e), X: xs[0], Index: idx}
	case *ast.StarExpr:
		return lw.expr(e.X)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.AND:
			return lw.lvalue(e.X)
		case token.ADD:
			return lw.expr(e.X)
		case token.SUB:
			return &Unary{T: lw.typeOf(e), Op: Neg, X: lw.expr(e.X)}
		case token.XOR:
			return &Unary{T: lw.typeOf(e), Op: BitComplement, X: lw.expr(e.X)}
		case token.NOT:
			return &Unary{T: Scalar(Bool), Op: Not, X: lw.expr(e.X)}
		}
	case *ast.BinaryExpr:
		return lw.binary(e)
	case *ast.CallExpr:
		return lw.call(e)
	case *ast.CompositeLit:
		return lw.composite(e)
	}
	return lw.unexpected(e.Pos(), "unexpected expression")
}

// unexpected reports a shape the validator admitted and the lowering
// has no form for, and yields a placeholder so lowering continues to
// the end of the run, where Lower returns every diagnostic.
func (lw *lowerer) unexpected(pos token.Pos, format string, args ...any) Expr {
	lw.diag.add(pos, format, args...)
	return &Const{T: Scalar(I32), Value: constant.MakeInt64(0)}
}

// selector lowers a field access or a qualified constant. A buffer
// held by a buffer group parameter is that buffer's own local.
func (lw *lowerer) selector(e *ast.SelectorExpr) Expr {
	sel := lw.pkg.Info.Selections[e]
	if sel == nil {
		return lw.unexpected(e.Pos(), "unexpected qualified identifier %s", e.Sel.Name)
	}
	if lw.holdsBuffers(lw.pkg.Info.Types[e].Type) {
		return lw.bufferArgs(e)[0]
	}
	idx := sel.Index()
	x := lw.expr(e.X)
	return &FieldRef{T: lw.typeOf(e), X: x, Field: idx[len(idx)-1]}
}

// holdsBuffers reports a buffer, a buffer group, a pointer to either,
// or an array of buffers.
func (lw *lowerer) holdsBuffers(t types.Type) bool {
	named := namedOf(elementOf(t))
	if named == nil {
		return false
	}
	_, buffer := lw.buffers[named.Obj()]
	return buffer || lw.groups[named.Obj()] != nil
}

// bufferLocals resolves a buffer operand to the parameter locals it
// denotes: a buffer parameter is its local, a group parameter its
// buffers' locals in the group's order, and a field or constant array
// element of a group the part of the group's locals it holds.
func (lw *lowerer) bufferLocals(e ast.Expr) []int {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		p := lw.pkg.Info.Uses[x].(*types.Var)
		if f, ok := lw.fields[p]; ok {
			return f
		}
		return []int{lw.locals[p]}
	case *ast.SelectorExpr:
		base := lw.bufferLocals(x.X)
		if base == nil {
			return nil
		}
		g := lw.group(lw.pkg.Info.Types[x.X].Type)
		slot := g.fields[lw.pkg.Info.Selections[x].Index()[0]]
		return base[slot.first : slot.first+slot.count]
	case *ast.IndexExpr:
		base := lw.bufferLocals(x.X)
		if base == nil {
			return nil
		}
		k, _ := constant.Int64Val(constant.ToInt(lw.pkg.Info.Types[x.Index].Value))
		n := int(lw.pkg.Info.Types[x.X].Type.Underlying().(*types.Array).Len())
		per := len(base) / n
		return base[int(k)*per : (int(k)+1)*per]
	}
	lw.diag.add(e.Pos(), "unexpected buffer operand")
	return nil
}

// bufferArgs is a buffer operand as the arguments it passes: one per
// buffer it denotes, or the placeholder for an operand the lowering
// has no form for (bufferLocals).
func (lw *lowerer) bufferArgs(e ast.Expr) []Expr {
	locals := lw.bufferLocals(e)
	if locals == nil {
		return []Expr{lw.unexpected(e.Pos(), "unexpected buffer operand")}
	}
	args := make([]Expr, 0, len(locals))
	for _, li := range locals {
		args = append(args, &LocalRef{T: lw.fn.Locals[li].Type, Index: li})
	}
	return args
}

// wordElement reports w[i] on a word buffer, a word read or written.
func (lw *lowerer) wordElement(e ast.Expr) bool {
	ix, ok := ast.Unparen(e).(*ast.IndexExpr)
	if !ok {
		return false
	}
	named := namedOf(lw.pkg.Info.Types[ix.X].Type)
	if named == nil {
		return false
	}
	b, ok := lw.buffers[named.Obj()]
	return ok && b.Word
}

// index lowers a buffer index to the uint32 the helpers take.
func (lw *lowerer) index(e ast.Expr) Expr {
	x := lw.canon(lw.expr(e))
	if c, ok := x.(*Const); ok {
		return &Const{T: Scalar(U32), Value: c.Value}
	}
	if x.Type() == Scalar(U32) {
		return x
	}
	return &Convert{T: Scalar(U32), X: x}
}

// load lowers a word read, w[i]. A word of an array of buffers chosen
// by a variable is read by a switch over the array's buffers into a
// temporary, which the validator permits only as the whole value of
// an assignment, so nothing else in the statement is evaluated
// between the choice and the read.
func (lw *lowerer) load(e *ast.IndexExpr) Expr {
	u32 := Scalar(U32)
	if arr, ok := ast.Unparen(e.X).(*ast.IndexExpr); ok && lw.pkg.Info.Types[arr.Index].Value == nil {
		bufs := lw.bufferArgs(arr.X)
		k := lw.canon(lw.hoist(lw.expr(arr.Index)))
		kt := lw.temp(k.Type())
		lw.emit(&VarDecl{Local: kt, Init: k})
		i := lw.hoist(lw.index(e.Index))
		it := lw.temp(u32)
		lw.emit(&VarDecl{Local: it, Init: i})
		res := lw.temp(u32)
		lw.emit(&VarDecl{Local: res, Init: zero(u32)})
		sw := &Switch{Tag: &LocalRef{T: k.Type(), Index: kt}}
		for c, b := range bufs {
			read := &OpCall{T: u32, Op: opLoad, Args: []Expr{b, &LocalRef{T: u32, Index: it}}}
			sw.Cases = append(sw.Cases, Case{
				Values: []Expr{&Const{T: k.Type(), Value: constant.MakeInt64(int64(c))}},
				Body:   &Block{Stmts: []Stmt{&Assign{Targets: []Expr{&LocalRef{T: u32, Index: res}}, Values: []Expr{read}}}},
			})
		}
		lw.emit(sw)
		return &LocalRef{T: u32, Index: res}
	}
	return &OpCall{T: u32, Op: opLoad, Args: []Expr{lw.bufferArgs(e.X)[0], lw.index(e.Index)}}
}

// store lowers a word write, w[i] = v, with the index lowered before
// the value as Go evaluates it. A nil value is one the caller has
// already evaluated, given as v.
func (lw *lowerer) store(e ast.Expr, value ast.Expr, v Expr) {
	ix := ast.Unparen(e).(*ast.IndexExpr)
	buf := lw.bufferArgs(ix.X)[0]
	is := []Expr{lw.index(ix.Index)}
	if value != nil {
		m := lw.mark()
		v = lw.canon(lw.expr(value))
		lw.keepOrder(m, is)
	}
	lw.storeAt(buf, is[0], v)
}

// storeAt emits the store of v at a lowered index of a word buffer.
func (lw *lowerer) storeAt(buf, index, v Expr) {
	lw.emit(&ExprStmt{X: lw.hoist(&OpCall{Op: opStore, Args: []Expr{buf, index, v}})})
}

// lvalue lowers an assignable path; the blank identifier yields nil.
func (lw *lowerer) lvalue(e ast.Expr) Expr {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return lw.lvalue(e.X)
	case *ast.Ident:
		if e.Name == "_" {
			return nil
		}
		li, _ := lw.localOf(e)
		return &LocalRef{T: lw.fn.Locals[li].Type, Index: li}
	case *ast.SelectorExpr:
		return lw.selector(e)
	case *ast.IndexExpr:
		// The path's calls precede the index's, as in the read form:
		// when the index emits statements, the calls in the path so
		// far are spilled ahead of them, the path keeping its shape.
		xs := []Expr{lw.lvalue(e.X)}
		m := lw.mark()
		idx := lw.canon(lw.expr(e.Index))
		lw.keepOrder(m, xs)
		return &IndexRef{T: lw.typeOf(e), X: xs[0], Index: idx}
	case *ast.StarExpr:
		return lw.lvalue(e.X)
	}
	return lw.unexpected(e.Pos(), "unexpected assignment target")
}

// binaryOp is the operator of a binary expression's token, reporting
// a token that has none.
func binaryOp(tok token.Token) (Op, bool) {
	switch tok {
	case token.ADD:
		return Add, true
	case token.SUB:
		return Sub, true
	case token.MUL:
		return Mul, true
	case token.QUO:
		return Div, true
	case token.REM:
		return Rem, true
	case token.AND:
		return BitAnd, true
	case token.OR:
		return BitOr, true
	case token.XOR:
		return BitXor, true
	case token.AND_NOT:
		return AndNot, true
	case token.SHL:
		return Shl, true
	case token.SHR:
		return Shr, true
	case token.EQL:
		return Eq, true
	case token.NEQ:
		return Ne, true
	case token.LSS:
		return Lt, true
	case token.LEQ:
		return Le, true
	case token.GTR:
		return Gt, true
	case token.GEQ:
		return Ge, true
	case token.LAND:
		return LogicalAnd, true
	case token.LOR:
		return LogicalOr, true
	}
	return Add, false
}

// binary lowers a binary expression, applying the operand
// canonicalization rules and rewriting a short-circuit operator whose
// right operand calls.
func (lw *lowerer) binary(e *ast.BinaryExpr) Expr {
	op, ok := binaryOp(e.Op)
	if !ok {
		return lw.unexpected(e.OpPos, "unexpected operator %s", e.Op)
	}
	if op == LogicalAnd || op == LogicalOr {
		x := lw.expr(e.X)
		rb := lw.push()
		y := lw.hoist(lw.expr(e.Y))
		lw.pop()
		if len(rb.Stmts) == 0 && countCalls(y) == 0 {
			return &Binary{T: Scalar(Bool), Op: op, X: x, Y: y}
		}
		// t := x; if t { t = y } (and) or if !t { t = y } (or).
		li := lw.temp(Scalar(Bool))
		lw.emit(&VarDecl{Local: li, Init: lw.hoist(x)})
		t := &LocalRef{T: Scalar(Bool), Index: li}
		var cond Expr = t
		if op == LogicalOr {
			cond = &Unary{T: Scalar(Bool), Op: Not, X: t}
		}
		rb.Stmts = append(rb.Stmts, &Assign{Targets: []Expr{t}, Values: []Expr{y}})
		lw.emit(&If{Cond: cond, Then: rb})
		return t
	}
	xs := []Expr{lw.expr(e.X)}
	m := lw.mark()
	y := lw.expr(e.Y)
	lw.keepOrder(m, xs)
	b := &Binary{T: lw.typeOf(e), Op: op, X: xs[0], Y: y}
	lw.applyOperandRules(b)
	return b
}

// applyOperandRules canonicalizes the operands whose stale high bits
// would change the result: both sides of comparisons, division,
// remainder, and right shift, and the count of a left shift.
func (lw *lowerer) applyOperandRules(b *Binary) {
	switch b.Op {
	case Div, Rem, Shr, Eq, Ne, Lt, Le, Gt, Ge:
		b.X = lw.canon(b.X)
		b.Y = lw.canon(b.Y)
	case Shl:
		b.Y = lw.canon(b.Y)
	}
}

// call lowers a conversion, a module function call, a method call
// (receiver first), or a call of a renderer operation's software form.
func (lw *lowerer) call(c *ast.CallExpr) Expr {
	fun := ast.Unparen(c.Fun)
	tv := lw.pkg.Info.Types[fun]
	if tv.IsType() {
		to := lw.typ(tv.Type)
		x := lw.canon(lw.expr(c.Args[0]))
		if x.Type() == to {
			return x
		}
		return &Convert{T: to, X: x}
	}
	if tv.IsBuiltin() {
		// len of a variable-length buffer, its bound length as a
		// uint32, which the validator has held to a conversion's
		// operand; any other len or cap is a constant.
		if fun.(*ast.Ident).Name == "len" {
			return &OpCall{T: Scalar(U32), Op: opLength, Args: lw.bufferArgs(c.Args[0])}
		}
		// min or max: any number of operands folds pairwise from the
		// left.
		t := lw.typeOf(c).(Scalar)
		op := builtins[fun.(*ast.Ident).Name][Kind(t)]
		xs := []Expr{lw.canon(lw.expr(c.Args[0]))}
		for _, a := range c.Args[1:] {
			m := lw.mark()
			y := lw.canon(lw.expr(a))
			lw.keepOrder(m, xs)
			xs[0] = &OpCall{T: t, Op: op, Args: []Expr{xs[0], y}}
		}
		return xs[0]
	}
	var fn *types.Func
	var recv ast.Expr
	switch f := fun.(type) {
	case *ast.Ident:
		fn = lw.pkg.Info.Uses[f].(*types.Func)
	case *ast.SelectorExpr:
		if sel := lw.pkg.Info.Selections[f]; sel != nil {
			fn = sel.Obj().(*types.Func)
			recv = f.X
		} else {
			fn = lw.pkg.Info.Uses[f.Sel].(*types.Func)
		}
	}
	sig := fn.Type().(*types.Signature)
	var args []Expr
	if recv != nil {
		if lw.holdsBuffers(sig.Recv().Type()) {
			args = append(args, lw.bufferArgs(recv)...)
		} else if _, ok := sig.Recv().Type().(*types.Pointer); ok {
			args = append(args, lw.lvalue(recv))
		} else {
			args = append(args, lw.canon(lw.expr(recv)))
		}
	}
	for i, a := range c.Args {
		if lw.holdsBuffers(sig.Params().At(i).Type()) {
			args = append(args, lw.bufferArgs(a)...)
			continue
		}
		m := lw.mark()
		var x Expr
		if _, ok := sig.Params().At(i).Type().(*types.Pointer); ok {
			if u, ok := ast.Unparen(a).(*ast.UnaryExpr); ok && u.Op == token.AND {
				x = lw.lvalue(u.X)
			} else {
				x = lw.expr(a)
			}
		} else {
			x = lw.canon(lw.expr(a))
		}
		lw.keepOrder(m, args)
		args = append(args, x)
	}
	var rt Type
	switch sig.Results().Len() {
	case 0:
		rt = nil
	case 1:
		rt = lw.typ(sig.Results().At(0).Type())
	default:
		rt = lw.typ(sig.Results())
	}
	if fn.Pkg() == lw.prog.Parent.Types {
		return lw.operation(c, fn, sig, rt, args)
	}
	fi, ok := lw.funcs[fn]
	if !ok {
		lw.diag.add(c.Pos(), "call of %s outside the module", fn.Name())
	}
	return &Call{T: rt, Func: fi, Args: args}
}

// operation lowers a call into the parent package to the operation of
// compute.Operations its name means, holding the call to the operation
// as the interface takes it, the operand count and the result, since
// Invoke passes the operands to a backend's method by position. The
// parent's declaration is what the call type-checked against; its
// body is the software renderer's and is never lowered.
func (lw *lowerer) operation(c *ast.CallExpr, fn *types.Func, sig *types.Signature, rt Type, args []Expr) Expr {
	name := fn.Name()
	op, ok := operations[name]
	if !ok {
		lw.diag.add(c.Pos(), "%s is not an operation of compute.Operations", name)
		return &OpCall{T: rt, Op: opLoad, Args: args}
	}
	operands := len(args)
	if op.Buffered {
		operands--
	}
	if operands != op.Operands {
		lw.diag.add(c.Pos(), "call of %s has %d operands, the operation takes %d", name, operands, op.Operands)
	}
	switch n := sig.Results().Len(); {
	case op.Yields && n != 1:
		lw.diag.add(c.Pos(), "call of %s has no result, the operation has one", name)
	case !op.Yields && n != 0:
		lw.diag.add(c.Pos(), "call of %s has a result, the operation has none", name)
	}
	return &OpCall{T: rt, Op: op, Args: args}
}

// composite lowers a struct or array literal to positional elements
// with zero fill.
func (lw *lowerer) composite(e *ast.CompositeLit) Expr {
	t := lw.typeOf(e)
	switch u := t.(type) {
	case StructRef:
		st := lw.mod.Structs[u.Index]
		elems := make([]Expr, len(st.Fields))
		for i, f := range st.Fields {
			elems[i] = zero(f.Type)
		}
		// The values in source order, Go's evaluation order, then
		// placed by field.
		vals := make([]Expr, 0, len(e.Elts))
		slots := make([]int, 0, len(e.Elts))
		for i, el := range e.Elts {
			idx := i
			val := el
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				name := kv.Key.(*ast.Ident).Name
				for j, f := range st.Fields {
					if f.Name == name {
						idx = j
					}
				}
				val = kv.Value
			}
			m := lw.mark()
			x := lw.canon(lw.expr(val))
			lw.keepOrder(m, vals)
			vals = append(vals, x)
			slots = append(slots, idx)
		}
		for j, x := range vals {
			elems[slots[j]] = x
		}
		return &Composite{T: t, Elems: elems}
	case ArrayType:
		elems := make([]Expr, u.Len)
		for i := range elems {
			elems[i] = zero(u.Elem)
		}
		vals := make([]Expr, 0, len(e.Elts))
		slots := make([]int, 0, len(e.Elts))
		idx := 0
		for _, el := range e.Elts {
			val := el
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				k, _ := constant.Int64Val(constant.ToInt(lw.pkg.Info.Types[kv.Key].Value))
				idx = int(k)
				val = kv.Value
			}
			m := lw.mark()
			x := lw.canon(lw.expr(val))
			lw.keepOrder(m, vals)
			vals = append(vals, x)
			slots = append(slots, idx)
			idx++
		}
		for j, x := range vals {
			elems[slots[j]] = x
		}
		return &Composite{T: t, Elems: elems}
	}
	lw.diag.add(e.Pos(), "unexpected composite literal type %s", t)
	return &Zero{T: t}
}

// canonical reports whether a narrow-typed expression is already in
// canonical (sign- or zero-extended) form. Wider types and bool are
// always canonical.
func canonical(e Expr) bool {
	s, ok := e.Type().(Scalar)
	if !ok || !s.Narrow() {
		return true
	}
	switch x := e.(type) {
	case *Binary:
		switch x.Op {
		case BitAnd, BitOr, BitXor, AndNot:
			return canonical(x.X) && canonical(x.Y)
		case Shr:
			return canonical(x.X)
		case Add, Sub, Mul, Shl, Div:
			// Division overflows in one case: the minimum divided
			// by minus one, which Go wraps back to the minimum.
			return false
		}
	case *Unary:
		return x.Op == Not
	}
	return true
}

// canon re-canonicalizes a narrow value at a point where its stale
// high bits would be observable.
func (lw *lowerer) canon(e Expr) Expr {
	if canonical(e) {
		return e
	}
	return &Mask{T: e.Type(), X: e}
}

// countCalls counts the calls in an expression tree whose order is
// observable: module calls and buffer helpers. The pure integer
// helpers have no effects and do not count.
func countCalls(e Expr) int {
	n := 0
	walkExpr(e, func(x Expr) {
		switch r := x.(type) {
		case *Call:
			n++
		case *OpCall:
			if !r.Op.Pure() {
				n++
			}
		}
	})
	return n
}

// walkExpr visits an expression tree in evaluation order.
func walkExpr(e Expr, f func(Expr)) {
	switch x := e.(type) {
	case *FieldRef:
		walkExpr(x.X, f)
	case *IndexRef:
		walkExpr(x.X, f)
		walkExpr(x.Index, f)
	case *TableRef:
		walkExpr(x.Index, f)
	case *Composite:
		for _, el := range x.Elems {
			walkExpr(el, f)
		}
	case *Call:
		for _, a := range x.Args {
			walkExpr(a, f)
		}
	case *OpCall:
		for _, a := range x.Args {
			walkExpr(a, f)
		}
	case *Unary:
		walkExpr(x.X, f)
	case *Binary:
		walkExpr(x.X, f)
		walkExpr(x.Y, f)
	case *Convert:
		walkExpr(x.X, f)
	case *Mask:
		walkExpr(x.X, f)
	}
	f(e)
}

// mark is the current block's statement count, taken before a later
// operand of an expression is lowered.
func (lw *lowerer) mark() int {
	return len(lw.blocks[len(lw.blocks)-1].Stmts)
}

// keepOrder keeps Go's left-to-right evaluation when lowering a later
// operand emitted statements since mark (a short-circuit rewrite or a
// word read from an array of buffers at a variable index):
// the calls of every earlier operand of the same expression are
// evaluated into temporaries declared ahead of those statements
// (spillAhead).
func (lw *lowerer) keepOrder(mark int, earlier []Expr) {
	if lw.mark() != mark {
		lw.spillAhead(mark, earlier)
	}
}

// spillAhead evaluates every call in each of the expressions into a
// temporary declared ahead of the statements emitted since mark, in
// Go's left-to-right order, and replaces the call in the expression
// with a reference to it. An expression keeps its shape around the
// references, so a reference argument's path to its target stays a
// path, with the calls in its indexes spilled.
func (lw *lowerer) spillAhead(mark int, exprs []Expr) {
	b := lw.blocks[len(lw.blocks)-1]
	tail := append([]Stmt(nil), b.Stmts[mark:]...)
	b.Stmts = b.Stmts[:mark]
	for i, e := range exprs {
		if e != nil && countCalls(e) > 0 {
			exprs[i] = lw.spillCalls(e, false)
		}
	}
	b.Stmts = append(b.Stmts, tail...)
}

// hoist introduces evaluation-order temporaries: when an expression
// holds more than one call, every call but the last is evaluated into
// a temporary ahead of the statement, in Go's left-to-right order.
func (lw *lowerer) hoist(e Expr) Expr {
	if countCalls(e) < 2 {
		return e
	}
	return lw.spillCalls(e, true)
}

// spillCalls evaluates the calls of an expression into temporaries
// ahead of the statement, in Go's left-to-right order, and returns
// the expression with each spilled call replaced by a reference to
// its temporary. The last call stays in place when keepLast is set.
func (lw *lowerer) spillCalls(e Expr, keepLast bool) Expr {
	keep := 0
	if keepLast {
		keep = countCalls(e)
	}
	seen := 0
	var rewrite func(e Expr) Expr
	rewrite = func(e Expr) Expr {
		switch x := e.(type) {
		case *FieldRef:
			return &FieldRef{T: x.T, X: rewrite(x.X), Field: x.Field}
		case *IndexRef:
			return &IndexRef{T: x.T, X: rewrite(x.X), Index: rewrite(x.Index)}
		case *TableRef:
			return &TableRef{T: x.T, Table: x.Table, Index: rewrite(x.Index)}
		case *Composite:
			n := &Composite{T: x.T, Elems: make([]Expr, len(x.Elems))}
			for i, el := range x.Elems {
				n.Elems[i] = rewrite(el)
			}
			return n
		case *Unary:
			return &Unary{T: x.T, Op: x.Op, X: rewrite(x.X)}
		case *Binary:
			return &Binary{T: x.T, Op: x.Op, X: rewrite(x.X), Y: rewrite(x.Y)}
		case *Convert:
			return &Convert{T: x.T, X: rewrite(x.X)}
		case *Mask:
			return &Mask{T: x.T, X: rewrite(x.X)}
		case *Call:
			n := &Call{T: x.T, Func: x.Func, Args: make([]Expr, len(x.Args))}
			for i, a := range x.Args {
				n.Args[i] = rewrite(a)
			}
			return lw.spill(n, &seen, keep)
		case *OpCall:
			n := &OpCall{T: x.T, Op: x.Op, Args: make([]Expr, len(x.Args))}
			for i, a := range x.Args {
				n.Args[i] = rewrite(a)
			}
			if x.Op.Pure() {
				return n
			}
			return lw.spill(n, &seen, keep)
		}
		return e
	}
	return rewrite(e)
}

// spill moves a call into a temporary, unless it is the call numbered
// keep, the last of its expression, which stays in place, or an
// operation with no result, which is a statement of its own.
func (lw *lowerer) spill(c Expr, seen *int, keep int) Expr {
	*seen++
	if *seen == keep || c.Type() == nil {
		return c
	}
	li := lw.temp(c.Type())
	lw.emit(&VarDecl{Local: li, Init: c})
	return &LocalRef{T: c.Type(), Index: li}
}
