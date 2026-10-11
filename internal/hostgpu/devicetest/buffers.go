// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package devicetest

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/user-none/erings/internal/hostgpu"
)

// The tests hold to the buffer kinds' access as the renderer does: a
// dispatch reads upload buffers and reads and writes working buffers,
// the CPU sets a working buffer's content through an upload buffer
// and a copy, and reads it through a copy into a readback buffer. An
// output buffer, which dispatches, copies, and fills write, the CPU
// reads directly.

// newBuffer allocates a buffer of a kind and releases it when the test
// ends. It holds the buffer to its size and its mapping to the kind's:
// size bytes for an upload, readback, or output buffer, none for a
// working buffer.
func newBuffer(tb testing.TB, dev hostgpu.Device, size int, kind hostgpu.BufferKind) hostgpu.Buffer {
	tb.Helper()
	b, err := dev.NewBuffer(size, kind)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(b.Release)
	mapped := size
	if kind == hostgpu.BufferWorking {
		mapped = 0
	}
	if len(b.Bytes()) != mapped || b.Size() != size {
		tb.Fatalf("buffer of kind %d and %d bytes: %d mapped, size %d, want %d mapped", kind, size, len(b.Bytes()), b.Size(), mapped)
	}
	return b
}

// submit encodes one submission and waits for it.
func submit(tb testing.TB, dev hostgpu.Device, encode func(cmds hostgpu.Commands)) {
	tb.Helper()
	cmds, err := dev.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	encode(cmds)
	if err := dev.Wait(cmds.Commit()); err != nil {
		tb.Fatal(err)
	}
}

// upload allocates an upload buffer holding data.
func upload(tb testing.TB, dev hostgpu.Device, data []byte) hostgpu.Buffer {
	tb.Helper()
	b := newBuffer(tb, dev, len(data), hostgpu.BufferUpload)
	copy(b.Bytes(), data)
	return b
}

// working allocates a working buffer of size bytes holding data,
// copied in from an upload buffer, or zero when data is nil, in a
// submission waited for at once.
func working(tb testing.TB, dev hostgpu.Device, size int, data []byte) hostgpu.Buffer {
	tb.Helper()
	b := newBuffer(tb, dev, size, hostgpu.BufferWorking)
	if data == nil {
		submit(tb, dev, func(cmds hostgpu.Commands) { cmds.Fill(b, 0, size, 0) })
		return b
	}
	up := upload(tb, dev, data)
	submit(tb, dev, func(cmds hostgpu.Commands) { cmds.Copy(b, 0, up, 0, len(data)) })
	return b
}

// ReadBack returns a copy of a working or upload buffer's bytes,
// copied into a readback buffer in a submission waited for at once.
func ReadBack(tb testing.TB, dev hostgpu.Device, b hostgpu.Buffer) []byte {
	tb.Helper()
	back := newBuffer(tb, dev, b.Size(), hostgpu.BufferReadback)
	submit(tb, dev, func(cmds hostgpu.Commands) { cmds.Copy(back, 0, b, 0, b.Size()) })
	return bytes.Clone(back.Bytes())
}

// wordBytes views words as their bytes.
func wordBytes(w []uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(w))), len(w)*4)
}

// valueBytes views a value as its bytes.
func valueBytes[T any](v *T) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(v)), int(unsafe.Sizeof(*v)))
}

// runToOut dispatches pipe over threads across one row, binding the
// inputs, a working output holding out, and params, in that order,
// and returns the output's bytes after the dispatch.
func runToOut(tb testing.TB, dev hostgpu.Device, pipe hostgpu.Pipeline, inputs []hostgpu.Buffer, out []byte, params hostgpu.Buffer, threads int) []byte {
	tb.Helper()
	outBuf := working(tb, dev, len(out), out)
	bindings := append(append([]hostgpu.Buffer{}, inputs...), outBuf, params)
	submit(tb, dev, func(cmds hostgpu.Commands) {
		cmds.Dispatch(pipe, bindings, hostgpu.Dispatch{BoundX: uint32(threads), BoundY: 1}, [2]int{threads, 1})
	})
	return ReadBack(tb, dev, outBuf)
}
