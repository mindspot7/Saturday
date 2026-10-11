// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

// TestIsEndCode checks the end code value per color mode.
func TestIsEndCode(t *testing.T) {
	cases := []struct {
		mode uint32
		end  uint32
		not  uint32
	}{{0, 0xF, 0xE}, {1, 0xF, 0}, {2, 0xFF, 0xFE}, {3, 0xFF, 0x7F}, {4, 0xFF, 0}, {5, 0x7FFF, 0xFFFF}}
	for _, c := range cases {
		if !IsEndCode(c.end, c.mode) {
			t.Errorf("mode %d: %#x is not an end code", c.mode, c.end)
		}
		if IsEndCode(c.not, c.mode) {
			t.Errorf("mode %d: %#x is an end code", c.mode, c.not)
		}
	}
	if IsEndCode(0xF, 6) || IsEndCode(0xFF, 7) {
		t.Error("the undefined modes have an end code")
	}
}

// TestEndCodeCounter checks the rule: the first end code does not
// end the line, a repeat of its source column is not counted, the
// second end code counted ends the line, and a fresh counter starts
// over, column 0 included.
func TestEndCodeCounter(t *testing.T) {
	var c EndCodeCounter
	c.Reset()
	steps := []struct {
		name   string
		column int32
		want   bool
	}{
		{"the first end code", 1, false},
		{"the same column again", 1, false},
		{"the second end code", 3, true},
	}
	for _, s := range steps {
		if got := c.Ends(s.column); got != s.want {
			t.Fatalf("%s: ends %v, want %v", s.name, got, s.want)
		}
	}
	if c.Count != 2 || c.Last != 4 {
		t.Errorf("counter %+v, want count 2 last 4", c)
	}
	c.Reset()
	if c.Ends(0) || c.Count != 1 {
		t.Errorf("after a reset the first end code at column 0 ended the line or count is %d", c.Count)
	}
	if c.Ends(0) || c.Count != 1 {
		t.Errorf("column 0 repeated counted again: count %d", c.Count)
	}
	if !c.Ends(1) {
		t.Error("the second column did not end the line")
	}
}
