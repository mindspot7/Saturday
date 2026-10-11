// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "testing"

// TestWindowRunState verifies the control byte evaluation over a run:
// the all-disabled identity, area inversion, the AND and OR operators,
// and the sprite window term joining per pixel.
func TestWindowRunState(t *testing.T) {
	cases := []struct {
		name       string
		wctl       uint32
		w0In, w1In bool
		swInside   bool
		want       bool
	}{
		{"all off OR", 0x00, true, true, true, false},
		{"all off AND", 0x80, false, false, false, true},
		{"W0 inside", 0x02, true, false, false, true},
		{"W0 inside, pixel outside", 0x02, false, false, false, false},
		{"W0 outside area", 0x03, false, false, false, true},
		{"W0 OR W1", 0x0A, false, true, false, true},
		{"W0 AND W1 one missing", 0x8A, true, false, false, false},
		{"W0 AND W1 both", 0x8A, true, true, false, true},
		{"sprite only inside", 0x20, false, false, true, true},
		{"sprite only outside area", 0x30, false, false, true, false},
		{"W0 OR sprite", 0x22, false, false, true, true},
		{"W0 AND sprite missing", 0xA2, true, false, false, false},
		{"W0 AND sprite both", 0xA2, true, false, true, true},
	}
	for _, tc := range cases {
		if got := WindowRunState(tc.wctl, tc.w0In, tc.w1In).Active(tc.swInside); got != tc.want {
			t.Errorf("%s: active = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestBuildWindowRuns verifies the row split: no windows, one window,
// two overlapping windows, and edges outside the row.
func TestBuildWindowRuns(t *testing.T) {
	var out WindowRuns
	BuildWindowRuns(LineWindow{}, LineWindow{}, 320, &out)
	if out.N != 1 || out.Runs[0] != (WindowRun{0, 320, false, false}) {
		t.Errorf("no windows: %v", out.Runs[:out.N])
	}
	BuildWindowRuns(LineWindow{true, 40, 199}, LineWindow{true, 100, 300}, 320, &out)
	want := []WindowRun{{0, 40, false, false}, {40, 100, true, false}, {100, 200, true, true}, {200, 301, false, true}, {301, 320, false, false}}
	if int(out.N) != len(want) {
		t.Fatalf("overlap: %d runs, want %d", out.N, len(want))
	}
	for i, w := range want {
		if out.Runs[i] != w {
			t.Errorf("overlap run %d = %v, want %v", i, out.Runs[i], w)
		}
	}
	BuildWindowRuns(LineWindow{true, -1, 352}, LineWindow{}, 320, &out)
	if out.N != 1 || out.Runs[0] != (WindowRun{0, 320, true, false}) {
		t.Errorf("edges outside the row: %v", out.Runs[:out.N])
	}
}
