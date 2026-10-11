// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"fmt"
	"go/types"
)

// importer resolves the one import the subset allows: the parent
// compute package for the subpackages. Anything else is an error,
// which the type checker reports at the import's position. The
// compile stage runs inside the emulator without a Go toolchain, so
// no standard library package can be loaded.
type importer struct {
	parentPath string
	parent     *types.Package
}

func (im *importer) Import(path string) (*types.Package, error) {
	if path == im.parentPath {
		if im.parent == nil {
			return nil, fmt.Errorf("compute parent package %s is not loaded", path)
		}
		return im.parent, nil
	}
	return nil, fmt.Errorf("import %q is outside the compute subset", path)
}
