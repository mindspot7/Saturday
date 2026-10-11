// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"testing"
)

// testFrame is a frame of the given width and height whose bytes
// identify their position, offset by seed.
func testFrame(width, height, seed int) (pixels []byte, stride int) {
	stride = width * 4
	pixels = make([]byte, stride*height)
	for i := range pixels {
		pixels[i] = byte(i*7 + seed)
	}
	return pixels, stride
}

// TestSharedFramebufferGrows holds frames larger than the starting
// size (a 2x hi-res frame, and a 2x double-density frame arriving
// between reads as a loaded state delivers it) to being read whole,
// a smaller frame after them to being read from the front of the
// grown buffers, and a snapshot an earlier read returned to being
// left as it was.
func TestSharedFramebufferGrows(t *testing.T) {
	sf := newSharedFramebuffer(maxFBWidth, maxFBHeight)
	small, smallStride := testFrame(352, 240, 1)
	sf.update(small, smallStride, 240)
	held, _, _ := sf.read()
	snapshot := append([]byte(nil), held[:smallStride*240]...)
	for _, f := range [][2]int{{1408, 448}, {1408, 1024}, {320, 224}} {
		want, stride := testFrame(f[0], f[1], f[0]+f[1])
		sf.update(want, stride, f[1])
		got, gotStride, gotHeight := sf.read()
		if gotStride != stride || gotHeight != f[1] {
			t.Fatalf("%dx%d: stride %d height %d", f[0], f[1], gotStride, gotHeight)
		}
		if len(got) < stride*f[1] || !bytes.Equal(got[:stride*f[1]], want) {
			t.Fatalf("%dx%d: the frame did not read back whole", f[0], f[1])
		}
	}
	if !bytes.Equal(held[:len(snapshot)], snapshot) {
		t.Fatal("the earlier read's snapshot changed")
	}
}
