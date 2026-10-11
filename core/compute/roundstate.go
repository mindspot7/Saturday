// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

// The round state of a VDP1 device draw is three variable-length word
// buffers a compute function takes as one RoundState: the order
// stamps, the fragment list heads, and the fragment pool. The host
// sizes each at renderer open to a power of two, the mask its helpers
// need. None holds a pixel: the frame buffer does. Each region's
// shared-word operation is in software.go with the other renderer
// operations a backend gives its own form.

// OrderStamps holds, per frame buffer byte, the draw order key of the
// last replace-class dot that landed on the pixel in the round, zero
// when none has. Its length is the frame buffer's bytes.
type OrderStamps []uint32

// FragmentHeads holds, per 16-bit pixel, the slot of the pixel's last
// appended fragment in the round, zero when none has. Its length is
// the frame buffer's 16-bit pixels.
type FragmentHeads []uint32

// FragmentPool holds the round's fragments, the destination-reading
// dots deferred to the resolve: word 0 is the reservation counter and
// the fragments follow, laid out by the draw package, so slot 0 is
// never a fragment and a zero link ends a list.
type FragmentPool []uint32

// RoundState is the round's three buffers as one parameter, a buffer
// group: the compile stage binds each field as its own buffer, in
// field order, and a compute function addresses a region through the
// field.
type RoundState struct {
	Stamps OrderStamps
	Heads  FragmentHeads
	Pool   FragmentPool
}
