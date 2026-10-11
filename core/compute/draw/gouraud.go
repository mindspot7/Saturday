// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

// LerpGouraud linearly interpolates a packed Gouraud entry (RGB555)
// between start and end at position i out of n-1 steps, each channel
// truncating toward zero.
func LerpGouraud(start, end uint32, i, n int32) uint32 {
	if n <= 1 {
		return start
	}
	sr := int32(start & 0x1F)
	sg := int32((start >> 5) & 0x1F)
	sb := int32((start >> 10) & 0x1F)
	er := int32(end & 0x1F)
	eg := int32((end >> 5) & 0x1F)
	eb := int32((end >> 10) & 0x1F)
	r := sr + (er-sr)*i/(n-1)
	g := sg + (eg-sg)*i/(n-1)
	b := sb + (eb-sb)*i/(n-1)
	return uint32(b)<<10 | uint32(g)<<5 | uint32(r)
}

// GouraudStepper interpolates a packed Gouraud value in fixed point
// with one addition per step: 16 fraction bits, plus the scale shift
// the stepper was made with.
type GouraudStepper struct {
	R, G, B    int32 // current values, 16 plus the scale shift fraction bits
	DR, DG, DB int32 // delta per step
}

// NewGouraudStepper starts at start and reaches end after n-1 steps,
// its values carried at shift more fraction bits than 16 (the scale
// shift, 0 native). The per-step delta truncates, so the last step
// may land one level below end when n-1 does not divide the channel
// difference. Over N times a native stepper's steps at the shift of
// N the delta is the native delta, so step N*i holds the native
// value of step i.
func NewGouraudStepper(start, end uint32, n int32, shift uint32) GouraudStepper {
	sr := int32(start & 0x1F)
	sg := int32((start >> 5) & 0x1F)
	sb := int32((start >> 10) & 0x1F)
	er := int32(end & 0x1F)
	eg := int32((end >> 5) & 0x1F)
	eb := int32((end >> 10) & 0x1F)
	frac := 16 + shift
	s := GouraudStepper{R: sr << frac, G: sg << frac, B: sb << frac}
	if n > 1 {
		s.DR = ((er - sr) << frac) / (n - 1)
		s.DG = ((eg - sg) << frac) / (n - 1)
		s.DB = ((eb - sb) << frac) / (n - 1)
	}
	return s
}

// clamp5 limits a channel to the 5-bit range.
func clamp5(v int32) uint32 {
	if v < 0 {
		return 0
	}
	if v > 0x1F {
		return 0x1F
	}
	return uint32(v)
}

// Value returns the current packed value with each channel clamped,
// for a stepper made with the scale shift.
func (s *GouraudStepper) Value(shift uint32) uint32 {
	frac := 16 + shift
	return clamp5(s.B>>frac)<<10 | clamp5(s.G>>frac)<<5 | clamp5(s.R>>frac)
}

// Step advances one step.
func (s *GouraudStepper) Step() {
	s.R += s.DR
	s.G += s.DG
	s.B += s.DB
}

// Advance applies n steps at once; the stepper is linear and clamping
// happens only in Value, so this equals n calls of Step.
func (s *GouraudStepper) Advance(n int32) {
	s.R += s.DR * n
	s.G += s.DG * n
	s.B += s.DB * n
}
