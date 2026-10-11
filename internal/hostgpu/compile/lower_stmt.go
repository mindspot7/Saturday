// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

func (lw *lowerer) stmts(list []ast.Stmt) {
	for _, s := range list {
		lw.stmt(s)
	}
}

// stmt lowers one statement into the block being built. Expression
// lowering may emit temporaries ahead of the statement.
func (lw *lowerer) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case nil, *ast.EmptyStmt:
	case *ast.BlockStmt:
		b := lw.push()
		lw.stmts(s.List)
		lw.pop()
		lw.emit(b)
	case *ast.DeclStmt:
		lw.declStmt(s.Decl.(*ast.GenDecl))
	case *ast.ExprStmt:
		x := lw.hoist(lw.expr(s.X))
		lw.emit(&ExprStmt{X: x})
	case *ast.AssignStmt:
		lw.assign(s)
	case *ast.IncDecStmt:
		op := Add
		if s.Tok == token.DEC {
			op = Sub
		}
		lw.compound(s.X, op, nil, &Const{T: lw.typeOf(s.X), Value: constant.MakeInt64(1)})
	case *ast.IfStmt:
		lw.ifStmt(s)
	case *ast.SwitchStmt:
		lw.switchStmt(s)
	case *ast.ForStmt:
		lw.forStmt(s)
	case *ast.RangeStmt:
		lw.rangeStmt(s)
	case *ast.BranchStmt:
		if s.Tok == token.BREAK {
			lw.emit(&Break{})
		} else {
			lw.emit(&Continue{})
		}
	case *ast.ReturnStmt:
		lw.returnStmt(s)
	default:
		lw.diag.add(s.Pos(), "unexpected statement")
	}
}

// declStmt lowers local var and const declarations. Constants are
// folded at their uses and declare nothing.
func (lw *lowerer) declStmt(gd *ast.GenDecl) {
	if gd.Tok != token.VAR {
		return
	}
	for _, spec := range gd.Specs {
		vs := spec.(*ast.ValueSpec)
		if len(vs.Values) == 1 && len(vs.Names) > 1 {
			lw.tupleDefine(vs.Names, vs.Values[0])
			continue
		}
		for i, n := range vs.Names {
			obj := lw.pkg.Info.Defs[n].(*types.Var)
			var init Expr
			if i < len(vs.Values) {
				init = lw.canon(lw.hoist(lw.expr(vs.Values[i])))
			}
			li := lw.newLocal(obj, n.Name)
			if init == nil {
				init = zero(lw.fn.Locals[li].Type)
			}
			lw.emit(&VarDecl{Local: li, Init: init})
		}
	}
}

// assign lowers the assignment forms: definition, single, parallel
// (through temporaries so every right side is evaluated before any
// store), multi-result call, and compound.
func (lw *lowerer) assign(s *ast.AssignStmt) {
	switch {
	case s.Tok == token.DEFINE:
		if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
			names := make([]*ast.Ident, len(s.Lhs))
			for i, l := range s.Lhs {
				names[i] = l.(*ast.Ident)
			}
			lw.tupleDefine(names, s.Rhs[0])
			return
		}
		if len(s.Lhs) == 1 {
			lw.define(s.Lhs[0].(*ast.Ident), lw.canon(lw.hoist(lw.expr(s.Rhs[0]))))
			return
		}
		values := lw.evalAll(s.Rhs)
		for i, l := range s.Lhs {
			lw.define(l.(*ast.Ident), values[i])
		}
	case s.Tok == token.ASSIGN:
		if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
			lw.tupleAssign(s.Lhs, s.Rhs[0])
			return
		}
		if len(s.Lhs) == 1 {
			if lw.wordElement(s.Lhs[0]) {
				lw.store(s.Lhs[0], s.Rhs[0], nil)
				return
			}
			// The target's path is lowered before the value, as Go
			// evaluates its index operands first: when the value calls
			// or emitted statements, every call in the path is spilled
			// ahead of them, and otherwise the path's own calls but the
			// last are.
			ts := []Expr{lw.lvalue(s.Lhs[0])}
			m := lw.mark()
			v := lw.expr(s.Rhs[0])
			if ts[0] == nil {
				// A blank target keeps only the value's effects: its
				// calls, and the statements lowering it emitted.
				if countCalls(v) > 0 {
					lw.emit(&ExprStmt{X: lw.canon(lw.hoist(v))})
				}
				return
			}
			if countCalls(v) > 0 || lw.mark() != m {
				lw.spillAhead(m, ts)
			} else {
				ts[0] = lw.hoist(ts[0])
			}
			lw.emit(&Assign{Targets: ts, Values: []Expr{lw.canon(lw.hoist(v))}})
			return
		}
		// Go's two phases: the targets' index operands, then the
		// values, then the stores in order.
		paths, bufs := lw.targetPlaces(s.Lhs)
		values := lw.evalAll(s.Rhs)
		for i := range s.Lhs {
			switch {
			case bufs[i] != nil:
				lw.storeAt(bufs[i], paths[i], values[i])
			case paths[i] != nil:
				lw.emit(&Assign{Targets: []Expr{paths[i]}, Values: []Expr{values[i]}})
			}
		}
	default:
		op, ok := compoundOp(s.Tok)
		if !ok {
			lw.diag.add(s.TokPos, "unexpected assignment operator %s", s.Tok)
			return
		}
		lw.compound(s.Lhs[0], op, s.Rhs[0], nil)
	}
}

// targetPlaces lowers the targets of an assignment with several
// targets ahead of its values, Go's first phase: each target's index
// operands are evaluated, into a temporary unless constant, so a
// store of the second phase changes no later target's place, and
// every call in them precedes the values' calls. A word element
// yields its buffer and its index, a blank nothing.
func (lw *lowerer) targetPlaces(lhs []ast.Expr) (paths, bufs []Expr) {
	paths, bufs = make([]Expr, len(lhs)), make([]Expr, len(lhs))
	for i, l := range lhs {
		if lw.wordElement(l) {
			ix := ast.Unparen(l).(*ast.IndexExpr)
			bufs[i], paths[i] = lw.bufferArgs(ix.X)[0], lw.placeIndex(lw.index(ix.Index))
		} else if t := lw.lvalue(l); t != nil {
			paths[i] = lw.placeIndexes(t)
		}
	}
	return paths, bufs
}

// placeIndexes evaluates every index of a target path into a
// temporary, the outer path's before the inner index's, leaving a
// constant index in place.
func (lw *lowerer) placeIndexes(e Expr) Expr {
	switch x := e.(type) {
	case *FieldRef:
		return &FieldRef{T: x.T, X: lw.placeIndexes(x.X), Field: x.Field}
	case *IndexRef:
		inner := lw.placeIndexes(x.X)
		return &IndexRef{T: x.T, X: inner, Index: lw.placeIndex(x.Index)}
	}
	return e
}

// placeIndex evaluates an index into a temporary unless it is a
// constant.
func (lw *lowerer) placeIndex(idx Expr) Expr {
	if _, ok := idx.(*Const); ok {
		return idx
	}
	idx = lw.hoist(idx)
	li := lw.temp(idx.Type())
	lw.emit(&VarDecl{Local: li, Init: idx})
	return &LocalRef{T: idx.Type(), Index: li}
}

// tupleAssign lowers targets = call() for a multi-result call: the
// targets' places first (targetPlaces), a blank target a temporary,
// then the call, whose results the Assign stores together. A word of
// a buffer is assigned through a temporary stored at its index after
// the call.
func (lw *lowerer) tupleAssign(lhs []ast.Expr, call ast.Expr) {
	tt := lw.typ(lw.pkg.Info.Types[call].Type).(Tuple)
	paths, bufs := lw.targetPlaces(lhs)
	c := lw.hoist(lw.expr(call))
	targets := make([]Expr, len(lhs))
	for i, p := range paths {
		targets[i] = p
		if bufs[i] != nil || p == nil {
			li := lw.temp(tt.Elems[i])
			lw.emit(&VarDecl{Local: li, Init: zero(tt.Elems[i])})
			targets[i] = &LocalRef{T: tt.Elems[i], Index: li}
		}
	}
	lw.emit(&Assign{Targets: targets, Values: []Expr{c}})
	for i, buf := range bufs {
		if buf != nil {
			lw.storeAt(buf, paths[i], targets[i])
		}
	}
}

// compoundOp is the operator of a compound assignment's token,
// reporting a token that has none.
func compoundOp(tok token.Token) (Op, bool) {
	switch tok {
	case token.ADD_ASSIGN:
		return Add, true
	case token.SUB_ASSIGN:
		return Sub, true
	case token.MUL_ASSIGN:
		return Mul, true
	case token.QUO_ASSIGN:
		return Div, true
	case token.REM_ASSIGN:
		return Rem, true
	case token.AND_ASSIGN:
		return BitAnd, true
	case token.OR_ASSIGN:
		return BitOr, true
	case token.XOR_ASSIGN:
		return BitXor, true
	case token.AND_NOT_ASSIGN:
		return AndNot, true
	case token.SHL_ASSIGN:
		return Shl, true
	case token.SHR_ASSIGN:
		return Shr, true
	}
	return Add, false
}

// define declares a new local from a short variable declaration. A
// blank name declares nothing: it reaches here only from a parallel
// definition, whose values evalAll has already evaluated into
// temporaries, so the value needs no statement of its own.
func (lw *lowerer) define(n *ast.Ident, v Expr) {
	if n.Name == "_" {
		return
	}
	obj, _ := lw.pkg.Info.Defs[n].(*types.Var)
	if obj == nil {
		// Redeclaration in a multi-define: an existing local.
		li, _ := lw.localOf(n)
		lw.emit(&Assign{Targets: []Expr{&LocalRef{T: lw.fn.Locals[li].Type, Index: li}}, Values: []Expr{v}})
		return
	}
	li := lw.newLocal(obj, n.Name)
	lw.emit(&VarDecl{Local: li, Init: v})
}

// tupleDefine lowers names := call() for a multi-result call: the
// locals are declared zero, then assigned together. A blank name
// defines a variable of its own, which go/types records like any
// other, so it is a local the Assign fills and nothing reads.
func (lw *lowerer) tupleDefine(names []*ast.Ident, call ast.Expr) {
	c := lw.hoist(lw.expr(call))
	tt := c.Type().(Tuple)
	targets := make([]Expr, len(names))
	for i, n := range names {
		var li int
		if obj, ok := lw.pkg.Info.Defs[n].(*types.Var); ok {
			li = lw.newLocal(obj, n.Name)
			lw.emit(&VarDecl{Local: li, Init: zero(tt.Elems[i])})
		} else {
			li, _ = lw.localOf(n)
		}
		targets[i] = &LocalRef{T: tt.Elems[i], Index: li}
	}
	lw.emit(&Assign{Targets: targets, Values: []Expr{c}})
}

// evalAll lowers several right-hand sides into temporaries in order.
func (lw *lowerer) evalAll(rhs []ast.Expr) []Expr {
	values := make([]Expr, len(rhs))
	for i, r := range rhs {
		v := lw.canon(lw.hoist(lw.expr(r)))
		li := lw.temp(v.Type())
		lw.emit(&VarDecl{Local: li, Init: v})
		values[i] = &LocalRef{T: v.Type(), Index: li}
	}
	return values
}

// compound lowers target op= value, the value given as source or, for
// an increment, already lowered as v. The target's path is lowered
// before the value, as Go evaluates its index operands first, and is
// fixed ahead of the value's statements (placeAhead), so the path is
// evaluated once and then read and written. A word of a buffer is
// read and written back at its index.
func (lw *lowerer) compound(target ast.Expr, op Op, value ast.Expr, v Expr) {
	if lw.wordElement(target) {
		ix := ast.Unparen(target).(*ast.IndexExpr)
		buf := lw.bufferArgs(ix.X)[0]
		is := []Expr{lw.index(ix.Index)}
		m := lw.mark()
		if value != nil {
			v = lw.expr(value)
		}
		lw.placeAhead(m, v, is, true)
		u32 := Scalar(U32)
		rhs := &Binary{T: u32, Op: op, X: &OpCall{T: u32, Op: opLoad, Args: []Expr{buf, is[0]}}, Y: v}
		lw.applyOperandRules(rhs)
		lw.storeAt(buf, is[0], rhs)
		return
	}
	ts := []Expr{lw.lvalue(target)}
	m := lw.mark()
	if value != nil {
		v = lw.expr(value)
	}
	lw.placeAhead(m, v, ts, false)
	rhs := &Binary{T: ts[0].Type(), Op: op, X: ts[0], Y: v}
	lw.applyOperandRules(rhs)
	lw.emit(&Assign{Targets: ts, Values: []Expr{lw.canon(lw.hoist(rhs))}})
}

// placeAhead fixes a compound target's place ahead of the statements
// emitted since mark. When the value calls or emitted statements, a
// call may write a variable an index reads, so every index is
// evaluated into a temporary there (placeIndex for a word's index,
// placeIndexes for a path). Otherwise only the calls in the indexes
// are spilled (spillAhead).
func (lw *lowerer) placeAhead(mark int, v Expr, place []Expr, word bool) {
	if countCalls(v) == 0 && lw.mark() == mark {
		lw.spillAhead(mark, place)
		return
	}
	b := lw.blocks[len(lw.blocks)-1]
	tail := append([]Stmt(nil), b.Stmts[mark:]...)
	b.Stmts = b.Stmts[:mark]
	if word {
		place[0] = lw.placeIndex(place[0])
	} else {
		place[0] = lw.placeIndexes(place[0])
	}
	b.Stmts = append(b.Stmts, tail...)
}

// ifStmt lowers an if with its optional init in an enclosing block.
// An else-if whose condition needs temporaries becomes an else block
// holding them and the nested if.
func (lw *lowerer) ifStmt(s *ast.IfStmt) {
	if s.Init != nil {
		b := lw.push()
		lw.stmt(s.Init)
		lw.emit(lw.ifCore(s))
		lw.pop()
		lw.emit(b)
		return
	}
	lw.emit(lw.ifCore(s))
}

func (lw *lowerer) ifCore(s *ast.IfStmt) Stmt {
	cond := lw.hoist(lw.expr(s.Cond))
	then := lw.push()
	lw.stmts(s.Body.List)
	lw.pop()
	st := &If{Cond: cond, Then: then}
	switch e := s.Else.(type) {
	case nil:
	case *ast.BlockStmt:
		eb := lw.push()
		lw.stmts(e.List)
		lw.pop()
		st.Else = eb
	case *ast.IfStmt:
		eb := lw.push()
		lw.ifStmt(e)
		lw.pop()
		if len(eb.Stmts) == 1 {
			if inner, ok := eb.Stmts[0].(*If); ok {
				st.Else = inner
				break
			}
		}
		st.Else = eb
	}
	return st
}

// switchStmt lowers a tagged switch with constant cases.
func (lw *lowerer) switchStmt(s *ast.SwitchStmt) {
	wrap := s.Init != nil
	var b *Block
	if wrap {
		b = lw.push()
		lw.stmt(s.Init)
	}
	tag := lw.canon(lw.hoist(lw.expr(s.Tag)))
	sw := &Switch{Tag: tag}
	for _, c := range s.Body.List {
		cc := c.(*ast.CaseClause)
		body := lw.push()
		lw.stmts(cc.Body)
		lw.pop()
		if cc.List == nil {
			sw.Default = body
			continue
		}
		cs := Case{Body: body}
		for _, e := range cc.List {
			cs.Values = append(cs.Values, &Const{T: tag.Type(), Value: lw.pkg.Info.Types[e].Value})
		}
		sw.Cases = append(sw.Cases, cs)
	}
	lw.emit(sw)
	if wrap {
		lw.pop()
		lw.emit(b)
	}
}

// forStmt lowers a loop. A condition that needs temporaries moves
// into the body as a conditional break, and a post statement that
// lowers to more than one statement is inlined at the end of the body
// and before each continue of this loop.
func (lw *lowerer) forStmt(s *ast.ForStmt) {
	wrap := s.Init != nil
	var b *Block
	if wrap {
		b = lw.push()
		lw.stmt(s.Init)
	}
	loop := &For{}

	var cond Expr
	condPre := lw.push()
	if s.Cond != nil {
		cond = lw.hoist(lw.expr(s.Cond))
	}
	lw.pop()

	var post []Stmt
	if s.Post != nil {
		pb := lw.push()
		lw.stmt(s.Post)
		lw.pop()
		post = pb.Stmts
	}

	body := lw.push()
	if len(condPre.Stmts) > 0 {
		body.Stmts = append(body.Stmts, condPre.Stmts...)
		body.Stmts = append(body.Stmts, &If{Cond: &Unary{T: Scalar(Bool), Op: Not, X: cond}, Then: &Block{Stmts: []Stmt{&Break{}}}})
		cond = nil
	}
	lw.stmts(s.Body.List)
	lw.pop()

	loop.Cond = cond
	// A post of one statement stays the loop's post, unless it is an
	// operation with no result: a backend emitting source prints such
	// an operation as a line of its own, so it joins the body's end
	// as a longer post does.
	if len(post) == 1 && !statementOperation(post[0]) {
		loop.Post = post[0]
	} else if len(post) > 0 {
		inlineContinues(body, post)
		body.Stmts = append(body.Stmts, post...)
	}
	loop.Body = body
	lw.emit(loop)
	if wrap {
		lw.pop()
		lw.emit(b)
	}
}

// rangeStmt lowers a range over an integer n: a hidden counter from
// zero while below n, evaluated once, with the key a fresh local per
// iteration set from the counter, so an assignment to the key in the
// body does not change the iterations, as in Go.
func (lw *lowerer) rangeStmt(s *ast.RangeStmt) {
	b := lw.push()
	n := lw.canon(lw.hoist(lw.expr(s.X)))
	t := n.Type()
	nl := lw.temp(t)
	lw.emit(&VarDecl{Local: nl, Init: n})
	cl := lw.temp(t)
	lw.emit(&VarDecl{Local: cl, Init: zero(t)})
	counter := &LocalRef{T: t, Index: cl}

	body := lw.push()
	if key, ok := s.Key.(*ast.Ident); ok && key.Name != "_" {
		if s.Tok == token.DEFINE {
			lw.define(key, counter)
		} else {
			lw.emit(&Assign{Targets: []Expr{lw.lvalue(key)}, Values: []Expr{counter}})
		}
	}
	lw.stmts(s.Body.List)
	lw.pop()

	next := lw.canon(&Binary{T: t, Op: Add, X: counter, Y: &Const{T: t, Value: constant.MakeInt64(1)}})
	lw.emit(&For{
		Cond: &Binary{T: Scalar(Bool), Op: Lt, X: counter, Y: &LocalRef{T: t, Index: nl}},
		Post: &Assign{Targets: []Expr{counter}, Values: []Expr{next}},
		Body: body,
	})
	lw.pop()
	lw.emit(b)
}

// statementOperation reports a statement that is an operation with no
// result.
func statementOperation(s Stmt) bool {
	es, ok := s.(*ExprStmt)
	if !ok {
		return false
	}
	r, ok := es.X.(*OpCall)
	return ok && !r.Op.Yields
}

// inlineContinues prefixes every continue of the current loop (not of
// nested loops) with the post statements.
func inlineContinues(b *Block, post []Stmt) {
	for i, s := range b.Stmts {
		switch x := s.(type) {
		case *Continue:
			b.Stmts[i] = &Block{Stmts: append(append([]Stmt{}, post...), x)}
		case *Block:
			inlineContinues(x, post)
		case *If:
			inlineContinues(x.Then, post)
			switch e := x.Else.(type) {
			case *Block:
				inlineContinues(e, post)
			case *If:
				inlineContinues(&Block{Stmts: []Stmt{e}}, post)
			}
		case *Switch:
			for _, c := range x.Cases {
				inlineContinues(c.Body, post)
			}
			if x.Default != nil {
				inlineContinues(x.Default, post)
			}
		}
	}
}

// returnStmt lowers a return: bare returns name the result locals, and
// a forwarded multi-result call goes through temporaries.
func (lw *lowerer) returnStmt(s *ast.ReturnStmt) {
	ret := &Return{}
	switch {
	case len(s.Results) == 0:
		for _, li := range lw.results {
			ret.Values = append(ret.Values, &LocalRef{T: lw.fn.Locals[li].Type, Index: li})
		}
	case len(s.Results) == 1 && len(lw.fn.Results) > 1:
		c := lw.hoist(lw.expr(s.Results[0]))
		tt := c.Type().(Tuple)
		targets := make([]Expr, len(tt.Elems))
		for i, t := range tt.Elems {
			li := lw.temp(t)
			lw.emit(&VarDecl{Local: li, Init: zero(t)})
			targets[i] = &LocalRef{T: t, Index: li}
		}
		lw.emit(&Assign{Targets: targets, Values: []Expr{c}})
		ret.Values = targets
	default:
		for _, r := range s.Results {
			m := lw.mark()
			v := lw.canon(lw.hoist(lw.expr(r)))
			lw.keepOrder(m, ret.Values)
			ret.Values = append(ret.Values, v)
		}
	}
	lw.emit(ret)
}
