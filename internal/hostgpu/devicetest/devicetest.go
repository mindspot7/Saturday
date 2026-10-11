// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package devicetest holds the device tests every backend runs on its
// own opened device, written once over the hostgpu interfaces. A
// backend's test file opens its device and calls them.
package devicetest

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// programs is the conformance program built on each device, once:
// every test of a device dispatches entries of the same module
// (conformance.AllSources), so one build per device serves them all.
var (
	programsMu sync.Mutex
	programs   = map[hostgpu.Device]*hostgpu.Program{}
)

// pipeline is the named conformance entry's pipeline on the device,
// from the program built on it at the first ask.
func pipeline(tb testing.TB, dev hostgpu.Device, name string) hostgpu.Pipeline {
	tb.Helper()
	programsMu.Lock()
	defer programsMu.Unlock()
	prog, ok := programs[dev]
	if !ok {
		parent, sub := conformance.AllSources()
		var err error
		if prog, err = hostgpu.Build(dev, parent, sub); err != nil {
			tb.Fatal(err)
		}
		programs[dev] = prog
	}
	pipe, err := prog.Pipeline(name)
	if err != nil {
		tb.Fatal(err)
	}
	return pipe
}

// deviceBuffers runs the variable word entry twice through a working
// buffer, from an upload buffer into it in one submission and from it
// into another working buffer in a second, and holds the result,
// copied out, to the Go execution run twice over the same input: a
// working buffer has no CPU mapping, binds, is written and read by
// dispatches, and holds its content across a submission.
func deviceBuffers(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableEntry)
	in, mid, out := conformance.DeviceInputs(conformance.VariableSizes[1])
	p := conformance.Probes()[0]
	inBuf := upload(t, dev, wordBytes(in))
	params := upload(t, dev, valueBytes(&p))
	midBuf := working(t, dev, len(mid)*4, wordBytes(mid))
	outBuf := working(t, dev, len(out)*4, wordBytes(out))
	conformance.RunDeviceGo(in, mid, out)
	run := func(bindings []hostgpu.Buffer, n int) {
		submit(t, dev, func(cmds hostgpu.Commands) {
			cmds.Dispatch(pipe, bindings, hostgpu.Dispatch{BoundX: uint32(n), BoundY: 1}, [2]int{n, 1})
		})
	}
	run([]hostgpu.Buffer{inBuf, midBuf, params}, len(mid))
	run([]hostgpu.Buffer{midBuf, outBuf, params}, len(out))
	if !bytes.Equal(ReadBack(t, dev, outBuf), wordBytes(out)) {
		t.Fatal("the round trip through the working buffer differs from Go")
	}
}

// dispatchOrder runs the variable word entry twice in one submission,
// from an upload buffer into a working buffer and from there into
// another, with a Barrier between the dispatches, and holds the
// result, copied out, to the Go execution run twice: the barrier
// orders one dispatch's writes ahead of the next dispatch's reads,
// the one edge a fill, a kernel on two backends and a transfer on
// the third, does not stand in for.
func dispatchOrder(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableEntry)
	in, mid, out := conformance.DeviceInputs(conformance.VariableSizes[1])
	p := conformance.Probes()[0]
	inBuf := upload(t, dev, wordBytes(in))
	params := upload(t, dev, valueBytes(&p))
	midBuf := working(t, dev, len(mid)*4, wordBytes(mid))
	outBuf := working(t, dev, len(out)*4, wordBytes(out))
	conformance.RunDeviceGo(in, mid, out)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Dispatch(pipe, []hostgpu.Buffer{inBuf, midBuf, params}, hostgpu.Dispatch{BoundX: uint32(len(mid)), BoundY: 1}, [2]int{len(mid), 1})
		cmds.Barrier()
		cmds.Dispatch(pipe, []hostgpu.Buffer{midBuf, outBuf, params}, hostgpu.Dispatch{BoundX: uint32(len(out)), BoundY: 1}, [2]int{len(out), 1})
	})
	if !bytes.Equal(ReadBack(t, dev, outBuf), wordBytes(out)) {
		t.Fatal("the dispatches ordered by a barrier differ from Go")
	}
}

// submissionOrder runs the variable word entry twice as dispatchOrder
// does, the dispatches in two submissions with no wait between them,
// and waits on the second alone: a submission's commands follow the
// earlier submission's writes. The largest variable size keeps the
// first dispatch running while the second is queued, so a backend
// that does not order them is likely to read stale words.
func submissionOrder(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableEntry)
	in, mid, out := conformance.DeviceInputs(conformance.VariableSizes[len(conformance.VariableSizes)-1])
	p := conformance.Probes()[0]
	inBuf := upload(t, dev, wordBytes(in))
	params := upload(t, dev, valueBytes(&p))
	midBuf := working(t, dev, len(mid)*4, wordBytes(mid))
	outBuf := working(t, dev, len(out)*4, wordBytes(out))
	back := newBuffer(t, dev, len(out)*4, hostgpu.BufferReadback)
	conformance.RunDeviceGo(in, mid, out)
	first, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first.Dispatch(pipe, []hostgpu.Buffer{inBuf, midBuf, params}, hostgpu.Dispatch{BoundX: uint32(len(mid)), BoundY: 1}, [2]int{len(mid), 1})
	first.Commit()
	second, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	second.Dispatch(pipe, []hostgpu.Buffer{midBuf, outBuf, params}, hostgpu.Dispatch{BoundX: uint32(len(out)), BoundY: 1}, [2]int{len(out), 1})
	second.Barrier()
	second.Copy(back, 0, outBuf, 0, len(out)*4)
	if err := dev.Wait(second.Commit()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Bytes(), wordBytes(out)) {
		t.Fatal("a submission's dispatch read differs from Go: it did not follow the earlier submission's writes")
	}
}

// outputBuffers runs the variable word entry from a working buffer
// into an output buffer, filled first in the same submission, then
// fills its first words again behind the dispatch, and holds the
// output buffer's own bytes after the wait to the Go execution with
// that fill over them: an output buffer binds, a dispatch and a fill
// write it, the barriers order them, and the CPU reads what they
// wrote with no copy.
func outputBuffers(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableEntry)
	in, out := conformance.VariableInputs(conformance.VariableSizes[1])
	p := conformance.Probes()[0]
	inBuf := working(t, dev, len(in)*4, wordBytes(in))
	params := upload(t, dev, valueBytes(&p))
	outBuf := newBuffer(t, dev, len(out)*4, hostgpu.BufferOutput)
	conformance.RunVariableGo(in, out)
	const refilled = 64
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Fill(outBuf, 0, len(out)*4, 0xA5A5A5A5)
		cmds.Barrier()
		cmds.Dispatch(pipe, []hostgpu.Buffer{inBuf, outBuf, params}, hostgpu.Dispatch{BoundX: uint32(len(out)), BoundY: 1}, [2]int{len(out), 1})
		cmds.Barrier()
		cmds.Fill(outBuf, 0, refilled, fillValue)
	})
	want := bytes.Clone(wordBytes(out))
	for i := 0; i < refilled; i += 4 {
		binary.LittleEndian.PutUint32(want[i:], fillValue)
	}
	if got := outBuf.Bytes(); !bytes.Equal(got, want) {
		for i := 0; i < len(want); i += 4 {
			if g, w := binary.LittleEndian.Uint32(got[i:]), binary.LittleEndian.Uint32(want[i:]); g != w {
				t.Fatalf("output buffer word at byte %d = %08x, want %08x", i, g, w)
			}
		}
	}
}
