// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
)

// Source is one compute package as the compile stage receives it: its
// import path and the file system holding its Go files. Files holds
// the package's subset source and nothing else: the compute packages
// name the files they embed one by one, so the stage takes every file
// it is given rather than filtering by name. Entries names the
// exported functions the renderer dispatches as compute entry points;
// every other function is a helper.
type Source struct {
	Path    string
	Files   fs.FS
	Entries []string
}

// parseSource parses the package's files in name order. Comments are
// kept so build constraints can be rejected.
func parseSource(fset *token.FileSet, src Source) ([]*ast.File, error) {
	entries, err := fs.ReadDir(src.Files, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		data, err := fs.ReadFile(src.Files, name)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(fset, src.Path+"/"+name, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}
