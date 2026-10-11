// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"strings"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
)

// TestComputePackages runs the compile stage's frontend on the real
// compute packages through their embedded source, so a subset
// violation in them fails the test suite rather than a session with
// the GPU renderer enabled.
func TestComputePackages(t *testing.T) {
	const root = "github.com/user-none/erings/core/compute"
	prog, err := Load(
		Source{Path: root, Files: compute.Source},
		Source{Path: root + "/screen", Files: screen.Source, Entries: screen.Entries[:]},
		Source{Path: root + "/draw", Files: draw.Source, Entries: draw.Entries[:]},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	if want := len(screen.Entries) + len(draw.Entries); len(mod.Entries) != want {
		t.Fatalf("entries = %d, want %d", len(mod.Entries), want)
	}
	for _, e := range mod.Entries {
		if e.Coords == 0 {
			t.Errorf("entry %s: coords=%d", mod.Funcs[e.Func].Name, e.Coords)
		}
	}
	dump := mod.Dump()
	if !strings.Contains(dump, "entry CompositeSpan coords=2") {
		t.Errorf("dump lacks the composite entry line")
	}
}
