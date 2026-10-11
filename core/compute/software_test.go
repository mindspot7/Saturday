// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"reflect"
	"testing"
)

// TestSoftwareFormsMatchOperations holds every function of the
// embedded source to the operation in Operations it is the software
// form of: its name is a method of Operations, it is a method on a buffer type
// exactly when the operation's first operand is the buffer, its
// parameters are the operation's value operands, and it has a result
// exactly when the operation does. The operand widths are the
// software form's to define, and the conformance rules hold every
// backend to them.
func TestSoftwareFormsMatchOperations(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := fs.ReadDir(Source, ".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		src, err := fs.ReadFile(Source, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, e.Name(), src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{}).Check("compute", fset, files, info); err != nil {
		t.Fatal(err)
	}
	ops := reflect.TypeFor[Operations[int, string]]()
	buffer := reflect.TypeFor[int]()
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fd.Name.Name
			m, ok := ops.MethodByName(name)
			if !ok {
				t.Errorf("%s: not an operation in Operations", name)
				continue
			}
			operands := m.Type.NumIn()
			buffered := operands > 0 && m.Type.In(0) == buffer
			if buffered {
				operands--
			}
			sig := info.Defs[fd.Name].(*types.Func).Type().(*types.Signature)
			switch recv := sig.Recv(); {
			case buffered && (recv == nil || !isBufferType(recv.Type())):
				t.Errorf("%s: acts on a buffer and must be a method on a buffer type", name)
			case !buffered && recv != nil:
				t.Errorf("%s: takes no buffer and must not be a method", name)
			}
			if n := sig.Params().Len(); n != operands {
				t.Errorf("%s: takes %d operands, the operation takes %d", name, n, operands)
			}
			if n := sig.Results().Len(); n != m.Type.NumOut() {
				t.Errorf("%s: has %d results, the operation has %d", name, n, m.Type.NumOut())
			}
		}
	}
}

// isBufferType reports a byte or word buffer type, by pointer or by
// value: a named array or slice of uint8 or uint32.
func isBufferType(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	var elem types.Type
	switch u := t.Underlying().(type) {
	case *types.Slice:
		elem = u.Elem()
	case *types.Array:
		elem = u.Elem()
	default:
		return false
	}
	b, ok := elem.Underlying().(*types.Basic)
	return ok && (b.Kind() == types.Uint8 || b.Kind() == types.Uint32)
}
