// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute_test

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/conformance"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
)

// TestEmbeddedSource holds each compute package's embedded source
// against its directory. The embed directives name their files one by
// one: a pattern takes the package's tests into the binary and hands
// them to the compile stage, which reads everything the embedded
// source holds. A subset file left out of a directive still builds
// and still runs in the software renderer, so the list is checked
// here rather than trusted.
func TestEmbeddedSource(t *testing.T) {
	for _, p := range []struct {
		dir   string
		files fs.FS
	}{
		{".", compute.Source},
		{"screen", screen.Source},
		{"draw", draw.Source},
		{"conformance", conformance.Source},
	} {
		t.Run(p.dir, func(t *testing.T) {
			want := subsetFiles(t, p.dir)
			got := embeddedFiles(t, p.files)
			if !slices.Equal(got, want) {
				t.Errorf("embedded %v, the directory's subset source is %v", got, want)
			}
		})
	}
}

// subsetFiles is a compute package directory's subset source: every Go
// file that is not a test, the embed declaration, or the operations
// interface.
func subsetFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "embed.go" || name == "operations.go" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// embeddedFiles is what a package's embed directives hold.
func embeddedFiles(t *testing.T, files fs.FS) []string {
	t.Helper()
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
