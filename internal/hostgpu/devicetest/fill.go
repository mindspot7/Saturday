// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package devicetest

import (
	"encoding/binary"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
)

// fillValue is the value the fill tests write: four different bytes,
// the opaque black the renderer fills its frame with among them, so a
// fill that writes a byte pattern rather than a word shows.
const fillValue = 0xFF000000

// fills runs the fill tests on the device: a fill at an offset into a
// working buffer ordered with copies behind barriers, and a fill wider
// than one dispatch of a backend whose API bounds it.
func fills(t *testing.T, dev hostgpu.Device) {
	t.Run("Offsets", func(t *testing.T) { fillOffsets(t, dev) })
	t.Run("Wide", func(t *testing.T) { fillWide(t, dev) })
}

// checkWords holds b's words: want inside [from, to) bytes and fill
// outside it.
func checkWords(t *testing.T, name string, b []byte, from, to int, want, fill uint32) {
	t.Helper()
	for i := 0; i < len(b); i += 4 {
		w := fill
		if i >= from && i < to {
			w = want
		}
		if got := binary.LittleEndian.Uint32(b[i:]); got != w {
			t.Fatalf("%s: word at byte %d = %08x, want %08x", name, i, got, w)
		}
	}
}

// fillOffsets lays down a working buffer's content with a copy, fills
// a range of it behind a barrier, copies it out behind another, and
// holds it to the value inside the range and the earlier content
// outside it. A fill of no bytes is issued beside it and must change
// nothing.
func fillOffsets(t *testing.T, dev hostgpu.Device) {
	const size = 1024
	data := make([]byte, size)
	for i := 0; i < size; i += 4 {
		binary.LittleEndian.PutUint32(data[i:], 0x55667788)
	}
	seed := upload(t, dev, data)
	mid := newBuffer(t, dev, size, hostgpu.BufferWorking)
	out := newBuffer(t, dev, size, hostgpu.BufferReadback)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Copy(mid, 0, seed, 0, size)
		cmds.Barrier()
		cmds.Fill(mid, 512, 256, fillValue)
		cmds.Fill(mid, 0, 0, fillValue)
		cmds.Barrier()
		cmds.Copy(out, 0, mid, 0, size)
	})
	checkWords(t, "working buffer", out.Bytes(), 512, 768, fillValue, 0x55667788)
}

// fillWideBytes is the wide fill's size: more words than one dispatch
// of 65535 thread groups of 64 threads covers, the DX12 limit and the
// Vulkan minimum.
const fillWideBytes = (65535*64 + 1024) * 4

// fillWide fills all but the first and last words of a working buffer
// wider than one dispatch covers and copies it out, holding every word
// of the range to the value and the words outside it to zero.
func fillWide(t *testing.T, dev hostgpu.Device) {
	mid := newBuffer(t, dev, fillWideBytes, hostgpu.BufferWorking)
	out := newBuffer(t, dev, fillWideBytes, hostgpu.BufferReadback)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Fill(mid, 0, fillWideBytes, 0)
		cmds.Barrier()
		cmds.Fill(mid, 4, fillWideBytes-8, fillValue)
		cmds.Barrier()
		cmds.Copy(out, 0, mid, 0, fillWideBytes)
	})
	checkWords(t, "wide fill", out.Bytes(), 4, fillWideBytes-4, fillValue, 0)
}
