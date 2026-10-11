// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package devicetest

import (
	"bytes"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// copies runs the copy tests on the device: copies at offsets from an
// upload buffer through working buffers into a readback buffer,
// ordered by barriers within one submission, copies into and out of
// an output buffer, and copies ordered with a dispatch on either side
// of it.
func copies(t *testing.T, dev hostgpu.Device) {
	t.Run("Offsets", func(t *testing.T) { copyOffsets(t, dev) })
	t.Run("Output", func(t *testing.T) { copyOutput(t, dev) })
	t.Run("Dispatch", func(t *testing.T) { copyDispatch(t, dev) })
}

// copyOutput fills an output buffer, copies a range of an upload
// buffer into it behind a barrier, and behind another copies it whole
// into a working buffer and into a readback buffer, then holds the
// output buffer's own bytes, the readback, and the working buffer
// copied out to the fill with the range over it: an output buffer is
// a copy destination and a copy source of either kind. The upload
// buffer is also copied straight into a readback buffer, the one copy
// no other test issues, and a copy of no bytes is issued and must
// change nothing.
func copyOutput(t *testing.T, dev hostgpu.Device) {
	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i*5 + 1)
	}
	src := upload(t, dev, data)
	out := newBuffer(t, dev, 1024, hostgpu.BufferOutput)
	mid := newBuffer(t, dev, 1024, hostgpu.BufferWorking)
	back := newBuffer(t, dev, 1024, hostgpu.BufferReadback)
	direct := newBuffer(t, dev, 512, hostgpu.BufferReadback)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Fill(out, 0, 1024, 0xA5A5A5A5)
		cmds.Barrier()
		cmds.Copy(out, 256, src, 0, 512)
		cmds.Copy(out, 0, src, 0, 0)
		cmds.Copy(direct, 0, src, 0, 512)
		cmds.Barrier()
		cmds.Copy(mid, 0, out, 0, 1024)
		cmds.Copy(back, 0, out, 0, 1024)
	})
	if !bytes.Equal(direct.Bytes(), data) {
		t.Fatal("the copy from the upload buffer into the readback buffer differs")
	}
	want := bytes.Repeat([]byte{0xA5}, 1024)
	copy(want[256:768], data)
	for _, c := range []struct {
		name string
		got  []byte
	}{{"output buffer", out.Bytes()}, {"readback", back.Bytes()}, {"working buffer", ReadBack(t, dev, mid)}} {
		if !bytes.Equal(c.got, want) {
			for i := range want {
				if c.got[i] != want[i] {
					t.Fatalf("%s: byte %d = %02x, want %02x", c.name, i, c.got[i], want[i])
				}
			}
		}
	}
}

// copyOffsets copies a range of an upload buffer into a working
// buffer at another offset and, behind a barrier in the same
// submission, from there into a second working buffer, filled first,
// at a third, then copies that one out, and holds it to the source's
// range with every byte outside the range keeping its fill.
func copyOffsets(t *testing.T, dev hostgpu.Device) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	src := upload(t, dev, data)
	mid := newBuffer(t, dev, 2048, hostgpu.BufferWorking)
	dst := newBuffer(t, dev, 1024, hostgpu.BufferWorking)
	back := newBuffer(t, dev, 1024, hostgpu.BufferReadback)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Fill(dst, 0, 1024, 0xA5A5A5A5)
		cmds.Copy(mid, 512, src, 128, 256)
		cmds.Barrier()
		cmds.Copy(dst, 64, mid, 512, 256)
		cmds.Barrier()
		cmds.Copy(back, 0, dst, 0, 1024)
	})
	want := bytes.Repeat([]byte{0xA5}, 1024)
	copy(want[64:320], data[128:384])
	if got := back.Bytes(); !bytes.Equal(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("byte %d = %02x, want %02x", i, got[i], want[i])
			}
		}
	}
}

// copyDispatch runs the variable word entry between two working
// buffers in one submission with copies on either side: the input and
// the output's fill copied in from upload buffers, a barrier, the
// dispatch, a barrier, and the output copied out to a readback
// buffer, held to the Go execution. A copy the barrier does not order
// with the dispatch shows as an input the entry did not read or an
// output the copy took before the entry wrote it.
func copyDispatch(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableEntry)
	in, out := conformance.VariableInputs(conformance.VariableSizes[1])
	p := conformance.Probes()[0]
	inUp := upload(t, dev, wordBytes(in))
	fillUp := upload(t, dev, wordBytes(out))
	params := upload(t, dev, valueBytes(&p))
	inDev := newBuffer(t, dev, len(in)*4, hostgpu.BufferWorking)
	outDev := newBuffer(t, dev, len(out)*4, hostgpu.BufferWorking)
	outBack := newBuffer(t, dev, len(out)*4, hostgpu.BufferReadback)
	conformance.RunVariableGo(in, out)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Copy(inDev, 0, inUp, 0, len(in)*4)
		cmds.Copy(outDev, 0, fillUp, 0, len(out)*4)
		cmds.Barrier()
		cmds.Dispatch(pipe, []hostgpu.Buffer{inDev, outDev, params}, hostgpu.Dispatch{BoundX: uint32(len(out)), BoundY: 1}, [2]int{len(out), 1})
		cmds.Barrier()
		cmds.Copy(outBack, 0, outDev, 0, len(out)*4)
	})
	if !bytes.Equal(outBack.Bytes(), wordBytes(out)) {
		t.Fatal("the copies around the dispatch differ from Go")
	}
}
