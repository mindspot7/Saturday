// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/token"
	"go/types"
)

// Load parses and type-checks the parent compute package and the
// compute subpackages, then validates the subpackages against the
// compute subset. Every diagnostic of the run is returned together as
// an ErrorList; a Program is returned only when there are none.
func Load(parent Source, subs ...Source) (*Program, error) {
	fset := token.NewFileSet()
	errs := &diagnostics{fset: fset}
	im := &importer{parentPath: parent.Path}

	parentPkg, err := typeCheck(fset, errs, im, parent)
	if err != nil {
		return nil, err
	}
	if errs.err() != nil {
		return nil, errs.err()
	}
	im.parent = parentPkg.Types

	prog := &Program{Fset: fset, Parent: parentPkg}
	for _, src := range subs {
		pkg, err := typeCheck(fset, errs, im, src)
		if err != nil {
			return nil, err
		}
		prog.Packages = append(prog.Packages, pkg)
	}
	if errs.err() != nil {
		return nil, errs.err()
	}

	v := newValidator(prog, errs)
	v.checkParent()
	for _, pkg := range prog.Packages {
		v.checkPackage(pkg)
	}
	if errs.err() != nil {
		return nil, errs.err()
	}
	return prog, nil
}

// typeCheck parses one package and type-checks it, reporting type
// diagnostics into errs. A parse failure is returned directly since nothing
// after it is meaningful.
func typeCheck(fset *token.FileSet, errs *diagnostics, im *importer, src Source) (*Package, error) {
	files, err := parseSource(fset, src)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		errs.add(token.NoPos, "%s: no Go source files", src.Path)
		return &Package{Path: src.Path}, nil
	}
	info := newInfo()
	conf := types.Config{
		Importer: im,
		Error: func(e error) {
			if te, ok := e.(types.Error); ok {
				errs.list = append(errs.list, Error{Pos: fset.Position(te.Pos), Msg: te.Msg})
				return
			}
			errs.list = append(errs.list, Error{Msg: e.Error()})
		},
	}
	pkg, _ := conf.Check(src.Path, fset, files, info)
	return &Package{Path: src.Path, Files: files, Types: pkg, Info: info, Entries: src.Entries}, nil
}

// funcDecls returns the package's function declarations in file order.
func funcDecls(files []*ast.File) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				out = append(out, fd)
			}
		}
	}
	return out
}
