// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package adapter

import (
	"testing"

	"github.com/user-none/eblitui/coreif"
	"github.com/user-none/erings/core"
)

// TestUpscaleOption checks the declared upscale option: a per-game
// video select whose default is one of its values, every value of
// which the core accepts.
func TestUpscaleOption(t *testing.T) {
	var opt *coreif.CoreOption
	opts := (&Factory{}).SystemInfo().CoreOptions
	for i := range opts {
		if opts[i].Key == "upscale" {
			opt = &opts[i]
		}
	}
	if opt == nil {
		t.Fatal("upscale option not declared")
	}
	if opt.Type != coreif.CoreOptionSelect {
		t.Fatalf("type %d", opt.Type)
	}
	if opt.Category != coreif.CoreOptionCategoryVideo {
		t.Fatalf("category %d", opt.Category)
	}
	if !opt.PerGame {
		t.Fatal("not per game")
	}
	if len(opt.Values) == 0 {
		t.Fatal("no values")
	}
	hasDefault := false
	emu := core.NewEmulator()
	for _, v := range opt.Values {
		if v == opt.Default {
			hasDefault = true
		}
		if err := emu.SetUpscale(v); err != nil {
			t.Fatalf("value %q: %v", v, err)
		}
	}
	if !hasDefault {
		t.Fatalf("default %q is not a value", opt.Default)
	}
}

// TestRenderInfoProvider checks that the adapter's emulator reports
// its renderer through coreif.RenderInfoProvider: software at native
// before any option, the scale following the upscale option, and a
// game frame count of zero before any frame runs.
func TestRenderInfoProvider(t *testing.T) {
	emu := (&Factory{}).CreateEmulator()
	defer emu.Close()
	p, ok := emu.(coreif.RenderInfoProvider)
	if !ok {
		t.Fatal("emulator does not implement RenderInfoProvider")
	}
	if got := p.RenderInfo(); got != (coreif.RenderInfo{Backend: "Software", Scale: 1}) {
		t.Fatalf("render info before options %+v", got)
	}
	emu.SetOption("upscale", "2x")
	if got := p.RenderInfo(); got != (coreif.RenderInfo{Backend: "Software", Scale: 2}) {
		t.Fatalf("render info at 2x %+v", got)
	}
	if n := p.GameFrameCount(); n != 0 {
		t.Fatalf("game frame count before any frame %d", n)
	}
}
