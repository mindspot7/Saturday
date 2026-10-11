// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dx12

import (
	"embed"
	"io/fs"
)

// placeholderBlob is the empty file committed in each generated
// directory so the embed directives compile before the generator has
// run. No entry point has its name, so it is never read as a blob.
const placeholderBlob = "placeholder.dxil"

// embedded is the renderer's compiled entry points, written by the
// generator (make dxil).
//
//go:embed dxil/renderer/*.dxil
var embedded embed.FS

// backendBlobs is the backend's own kernels, written by the generator
// (make dxil): the fill.
//
//go:embed dxil/backend/*.dxil
var backendBlobs embed.FS

// RendererShaders is the renderer's compiled entry points by name, or
// the reporting set when the directory holds only the placeholder.
func RendererShaders() fs.FS {
	sub, err := fs.Sub(embedded, "dxil/renderer")
	if err != nil || !hasBlobs(sub) {
		return missing{}
	}
	return sub
}

// hasBlobs reports whether the set holds a file other than the
// placeholder.
func hasBlobs(set fs.FS) bool {
	entries, err := fs.ReadDir(set, ".")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != placeholderBlob {
			return true
		}
	}
	return false
}
