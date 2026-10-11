// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// TestSetUpscale checks that the upscale value selects the renderer:
// 1x the accurate software renderer, each value above it the
// enhanced renderer at its shift, an unknown value rejected with the
// active renderer kept, and the option key reaching the same path.
func TestSetUpscale(t *testing.T) {
	e := NewEmulator()
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("default renderer is %T", e.renderer)
	}
	if err := e.SetUpscale("3x"); err == nil {
		t.Fatal("unknown upscale value accepted")
	}
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("renderer after an unknown value is %T", e.renderer)
	}

	names := UpscaleNames()
	if names[0] != "1x" {
		t.Fatalf("first upscale name %q", names[0])
	}
	for i, name := range names[1:] {
		if err := e.SetUpscale(name); err != nil {
			t.Fatal(err)
		}
		r, ok := e.renderer.(*enhancedRenderer)
		if !ok {
			t.Fatalf("%s: renderer is %T", name, e.renderer)
		}
		if r.shift != uint32(i+1) {
			t.Fatalf("%s: shift %d", name, r.shift)
		}
		if got := e.RendererName(); got != "Software "+name {
			t.Fatalf("%s: renderer name %q", name, got)
		}
		if e.Renderer() != RendererSoftware {
			t.Fatalf("%s: renderer mode %d", name, e.Renderer())
		}
	}

	if err := e.SetUpscale("1x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("renderer at 1x is %T", e.renderer)
	}

	if got := e.RenderInfo(); got != (RenderInfo{Backend: "Software", Scale: 1}) {
		t.Fatalf("render info at 1x %+v", got)
	}

	e.SetOption("upscale", "4x")
	if got := e.RendererName(); got != "Software 4x" {
		t.Fatalf("renderer name through the option %q", got)
	}
	if got := e.RenderInfo(); got != (RenderInfo{Backend: "Software", Scale: 4}) {
		t.Fatalf("render info at 4x %+v", got)
	}
	e.SetOption("upscale", "0x")
	if got := e.RendererName(); got != "Software 4x" {
		t.Fatalf("renderer name after an unknown option value %q", got)
	}
}
