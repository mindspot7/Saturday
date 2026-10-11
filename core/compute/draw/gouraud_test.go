// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "testing"

// TestLerpGouraud verifies the endpoints, a midpoint truncating toward
// zero per channel, and the single-step case.
func TestLerpGouraud(t *testing.T) {
	start, end := uint32(0x0000), uint32(0x7FFF)
	if LerpGouraud(start, end, 0, 32) != start || LerpGouraud(start, end, 31, 32) != end {
		t.Error("endpoints")
	}
	if got := LerpGouraud(start, end, 15, 32); got != 15<<10|15<<5|15 {
		t.Errorf("midpoint = 0x%04X", got)
	}
	if LerpGouraud(start, end, 0, 1) != start {
		t.Error("single step returns start")
	}
}

// TestGouraudStepper verifies stepping reaches the end value, Advance
// equals repeated Step, and the channel clamp.
func TestGouraudStepper(t *testing.T) {
	s := NewGouraudStepper(0x0000, 0x7FFF, 32, 0)
	for i := 0; i < 31; i++ {
		s.Step()
	}
	if got := s.Value(0); got != 0x7FFF {
		t.Errorf("after 31 steps = 0x%04X, want 0x7FFF", got)
	}
	a := NewGouraudStepper(0x0010, 0x001F, 16, 0)
	b := a
	for i := 0; i < 7; i++ {
		a.Step()
	}
	b.Advance(7)
	if a != b {
		t.Error("Advance differs from repeated Step")
	}
	c := GouraudStepper{R: -1 << 16, G: 40 << 16}
	if got := c.Value(0); got != 0x1F<<5 {
		t.Errorf("clamp = 0x%04X, want 0x03E0", got)
	}
	if NewGouraudStepper(0x0010, 0x001F, 1, 0).DR != 0 {
		t.Error("n <= 1 has no delta")
	}
}

// TestGouraudStepperScaled holds a stepper at scale shifts 1 to 3,
// over N times a native stepper's steps, to the native stepper at
// every block corner: step N*i holds the native value of step i, and
// the steps between hold values between the corners'. A divided step
// or a lost fraction bit moves a corner's value.
func TestGouraudStepperScaled(t *testing.T) {
	pairs := [][2]uint32{{0x4210, 0x421F}, {0x7C10, 0x0210}, {0x0000, 0x7FFF}, {0x7FFF, 0x0000}, {0x1234, 0x1234}}
	channel := func(v uint32, c uint32) int32 { return int32(v >> (5 * c) & 0x1F) }
	for _, p := range pairs {
		for n := int32(2); n < 300; n += 13 {
			native := NewGouraudStepper(p[0], p[1], n, 0)
			for shift := uint32(1); shift <= 3; shift++ {
				k := int32(1) << shift
				scaled := NewGouraudStepper(p[0], p[1], k*(n-1)+1, shift)
				for i := int32(0); i < n; i++ {
					a, b := native, native
					a.Advance(i)
					b.Advance(min(i+1, n-1))
					subs := k
					if i == n-1 {
						// The scaled stepper ends at the last corner.
						subs = 1
					}
					for r := range subs {
						s := scaled
						s.Advance(k*i + r)
						got := s.Value(shift)
						if r == 0 && got != a.Value(0) {
							t.Fatalf("pair %#x n %d shift %d step %d: %#04x, want the native %#04x", p, n, shift, i, got, a.Value(0))
						}
						for c := range uint32(3) {
							lo, hi := channel(a.Value(0), c), channel(b.Value(0), c)
							if v := channel(got, c); v < min(lo, hi) || v > max(lo, hi) {
								t.Fatalf("pair %#x n %d shift %d step %d+%d/%d: channel %d = %d, outside %d..%d", p, n, shift, i, r, k, c, v, lo, hi)
							}
						}
					}
				}
			}
		}
	}
}
