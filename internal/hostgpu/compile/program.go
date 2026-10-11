// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/token"
	"go/types"
)

// Package is one type-checked compute package.
type Package struct {
	Path    string
	Files   []*ast.File
	Types   *types.Package
	Info    *types.Info
	Entries []string
}

// Program is the loaded compute tree: the parent package and the
// validated subpackages, sharing one file set.
type Program struct {
	Fset     *token.FileSet
	Parent   *Package
	Packages []*Package
}

func newInfo() *types.Info {
	return &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
}
