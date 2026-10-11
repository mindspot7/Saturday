// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"fmt"
	"strings"
)

// Dump renders the module as text for the compile stage's tests:
// structs with offsets, tables, and each function with its locals and
// body. Locals print as their name with the index, temporaries as
// t<index>.
func (m *Module) Dump() string {
	var b strings.Builder
	for i, st := range m.Structs {
		fmt.Fprintf(&b, "struct#%d %s size=%d align=%d\n", i, st.Name, st.Size, st.Align)
		for _, f := range st.Fields {
			fmt.Fprintf(&b, "  %s %s @%d\n", f.Name, f.Type, f.Offset)
		}
	}
	for i, t := range m.Tables {
		fmt.Fprintf(&b, "table#%d %s %s\n", i, t.Name, t.Type)
	}
	for i := range m.Funcs {
		d := &dumper{m: m, fn: &m.Funcs[i], b: &b}
		d.function(i)
	}
	for _, e := range m.Entries {
		fmt.Fprintf(&b, "entry %s coords=%d", m.Funcs[e.Func].Name, e.Coords)
		for _, bb := range e.Buffers {
			fmt.Fprintf(&b, " buffer%d=%s", bb.Binding, bb.Type.Name)
		}
		for _, p := range e.Params {
			fmt.Fprintf(&b, " params=%s", m.Funcs[e.Func].Locals[p].Name)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

type dumper struct {
	m  *Module
	fn *Func
	b  *strings.Builder
}

func (d *dumper) function(i int) {
	fmt.Fprintf(d.b, "func#%d %s(", i, d.fn.Name)
	for p := 0; p < d.fn.NumParams; p++ {
		if p > 0 {
			d.b.WriteString(", ")
		}
		l := d.fn.Locals[p]
		kind := ""
		if l.Kind == RefParam {
			kind = "&"
		}
		fmt.Fprintf(d.b, "%s%s %s", kind, d.local(p), l.Type)
	}
	d.b.WriteString(")")
	for _, r := range d.fn.Results {
		fmt.Fprintf(d.b, " %s", r)
	}
	d.b.WriteString("\n")
	d.block(d.fn.Body, 1)
}

func (d *dumper) local(i int) string {
	l := d.fn.Locals[i]
	if l.Name == "" {
		return fmt.Sprintf("t%d", i)
	}
	return fmt.Sprintf("%s#%d", l.Name, i)
}

func (d *dumper) block(b *Block, depth int) {
	for _, s := range b.Stmts {
		d.stmt(s, depth)
	}
}

func (d *dumper) line(depth int, format string, args ...any) {
	d.b.WriteString(strings.Repeat("  ", depth))
	fmt.Fprintf(d.b, format, args...)
	d.b.WriteByte('\n')
}

func (d *dumper) stmt(s Stmt, depth int) {
	switch s := s.(type) {
	case *Block:
		d.line(depth, "{")
		d.block(s, depth+1)
		d.line(depth, "}")
	case *VarDecl:
		d.line(depth, "var %s %s = %s", d.local(s.Local), d.fn.Locals[s.Local].Type, d.expr(s.Init))
	case *Assign:
		d.line(depth, "%s = %s", d.exprs(s.Targets), d.exprs(s.Values))
	case *ExprStmt:
		d.line(depth, "%s", d.expr(s.X))
	case *If:
		d.line(depth, "if %s {", d.expr(s.Cond))
		d.block(s.Then, depth+1)
		switch e := s.Else.(type) {
		case *Block:
			d.line(depth, "} else {")
			d.block(e, depth+1)
		case *If:
			d.line(depth, "} else")
			d.stmt(e, depth)
			return
		}
		d.line(depth, "}")
	case *Switch:
		d.line(depth, "switch %s {", d.expr(s.Tag))
		for _, c := range s.Cases {
			d.line(depth, "case %s:", d.exprs(c.Values))
			d.block(c.Body, depth+1)
		}
		if s.Default != nil {
			d.line(depth, "default:")
			d.block(s.Default, depth+1)
		}
		d.line(depth, "}")
	case *For:
		cond, post := "", ""
		if s.Cond != nil {
			cond = d.expr(s.Cond)
		}
		if s.Post != nil {
			post = d.inline(s.Post)
		}
		d.line(depth, "for ; %s; %s {", cond, post)
		d.block(s.Body, depth+1)
		d.line(depth, "}")
	case *Break:
		d.line(depth, "break")
	case *Continue:
		d.line(depth, "continue")
	case *Return:
		d.line(depth, "return %s", d.exprs(s.Values))
	}
}

// inline renders a simple statement on one line for loop headers.
func (d *dumper) inline(s Stmt) string {
	var b strings.Builder
	sub := &dumper{m: d.m, fn: d.fn, b: &b}
	sub.stmt(s, 0)
	return strings.TrimSpace(b.String())
}

func (d *dumper) exprs(list []Expr) string {
	parts := make([]string, len(list))
	for i, e := range list {
		parts[i] = d.expr(e)
	}
	return strings.Join(parts, ", ")
}

func (d *dumper) expr(e Expr) string {
	switch x := e.(type) {
	case *Const:
		return x.Value.ExactString()
	case *LocalRef:
		return d.local(x.Index)
	case *FieldRef:
		return d.expr(x.X) + "." + d.fieldName(x)
	case *IndexRef:
		return d.expr(x.X) + "[" + d.expr(x.Index) + "]"
	case *TableRef:
		return d.m.Tables[x.Table].Name + "[" + d.expr(x.Index) + "]"
	case *Zero:
		return "zero(" + x.T.String() + ")"
	case *Composite:
		return x.T.String() + "{" + d.exprs(x.Elems) + "}"
	case *Call:
		return d.m.Funcs[x.Func].Name + "(" + d.exprs(x.Args) + ")"
	case *OpCall:
		return "op." + x.Op.String() + "(" + d.exprs(x.Args) + ")"
	case *Unary:
		return "(" + x.Op.String() + " " + d.expr(x.X) + ")"
	case *Binary:
		return "(" + d.expr(x.X) + " " + x.Op.String() + " " + d.expr(x.Y) + ")"
	case *Convert:
		return x.T.String() + "(" + d.expr(x.X) + ")"
	case *Mask:
		return "mask." + x.T.String() + "(" + d.expr(x.X) + ")"
	case *LoadStruct:
		return "load." + x.T.String() + "(" + d.expr(x.Buffer) + ", " + d.expr(x.Addr) + ")"
	}
	return "?"
}

func (d *dumper) fieldName(f *FieldRef) string {
	if sr, ok := f.X.Type().(StructRef); ok {
		return d.m.Structs[sr.Index].Fields[f.Field].Name
	}
	return fmt.Sprintf("f%d", f.Field)
}
