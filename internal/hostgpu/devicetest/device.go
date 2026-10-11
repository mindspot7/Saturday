// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package devicetest

import (
	"encoding/binary"
	"errors"
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// wordsPipeline builds the Words conformance entry, one word per
// thread, the transport shape of a round trip.
func wordsPipeline(tb testing.TB, dev hostgpu.Device) (hostgpu.Pipeline, hostgpu.Dispatch) {
	tb.Helper()
	return pipeline(tb, dev, "Words"), hostgpu.Dispatch{BoundX: conformance.Threads, BoundY: 1}
}

// wordsBuffers allocates the Words entry's four buffers with the
// conformance inputs, the input and the probes as upload buffers and
// the byte buffer and the output as working buffers, and returns the
// inputs with them.
func wordsBuffers(tb testing.TB, dev hostgpu.Device) ([]hostgpu.Buffer, compute.Layer, *compute.VDP2VRAM) {
	tb.Helper()
	in := make(compute.Layer, conformance.LayerWords)
	mem := new(compute.VDP2VRAM)
	conformance.Inputs(in, mem)
	probes := conformance.Probes()
	return []hostgpu.Buffer{
		upload(tb, dev, wordBytes(in)),
		working(tb, dev, len(mem), mem[:]),
		working(tb, dev, conformance.LayerWords*4, nil),
		upload(tb, dev, valueBytes(&probes)),
	}, in, mem
}

// RoundTrip dispatches the Words entry over an overshooting grid,
// waits, and reads the output back through a readback buffer, holding
// every word of it to the Go execution: the words past the bound stay
// zero, so a wrapper that let a thread past the bound run would show.
func RoundTrip(t *testing.T, dev hostgpu.Device) {
	pipe, rec := wordsPipeline(t, dev)
	buffers, in, mem := wordsBuffers(t, dev)
	submit(t, dev, func(cmds hostgpu.Commands) {
		cmds.Dispatch(pipe, buffers, rec, [2]int{conformance.Threads + 100, 1})
	})
	want := make(compute.Layer, conformance.LayerWords)
	probes := conformance.Probes()
	for _, e := range conformance.Entries {
		if e.Name == "Words" {
			conformance.RunGo(e, in, mem, want, &probes)
		}
	}
	out := ReadBack(t, dev, buffers[2])
	for w := range want {
		if got := binary.LittleEndian.Uint32(out[w*4:]); got != want[w] {
			t.Fatalf("word %d (thread %d) = %08x, want %08x", w, w/8, got, want[w])
		}
	}
}

// SubmissionsReleased checks the submission contract on a freshly
// opened device: a Begin while a submission is being encoded is
// refused, sequence numbers rise by one per commit from 1, a wait on
// a later submission completes an earlier one not waited on, so a
// wait on the earlier one after it reports nothing, and a wait on a
// sequence number no commit has returned is an error. The release of
// the earlier submission is not observable through the device; it is
// the ledger's, which ledger_test.go holds.
func SubmissionsReleased(t *testing.T, dev hostgpu.Device) {
	first, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Begin(); err == nil {
		t.Fatal("a second Begin while encoding succeeded")
	}
	s1 := first.Commit()
	second, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	s2 := second.Commit()
	if s1 != 1 || s2 != s1+1 {
		t.Fatalf("submissions %d and %d, want consecutive from 1", s1, s2)
	}
	if err := dev.Wait(s2); err != nil {
		t.Fatal(err)
	}
	if err := dev.Wait(s1); err != nil {
		t.Fatalf("wait on a submission completed by a later wait: %v", err)
	}
	if err := dev.Wait(s2 + 1); err == nil {
		t.Fatal("wait before commit succeeded")
	}
	third, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if s3 := third.Commit(); s3 != s2+1 {
		t.Fatalf("third submission %d, want %d", s3, s2+1)
	} else if err := dev.Wait(s3); err != nil {
		t.Fatal(err)
	}
}

// CloseWhileEncoding leaves a submission being encoded, with a
// command recorded, for the caller's cleanup to close the device on:
// Close abandons the encoder rather than submitting it, and a Close
// that panics or blocks on it fails the test. What Close releases is
// not observable through the device.
func CloseWhileEncoding(t *testing.T, dev hostgpu.Device) {
	buf := newBuffer(t, dev, 256, hostgpu.BufferWorking)
	cmds, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	cmds.Fill(buf, 0, 256, 0)
}

// EncodeAfterCommit checks that a command on a committed submission
// is refused as the contract says: it is not encoded, the next Begin
// reports ErrClosed, and the Begin after that opens a submission.
func EncodeAfterCommit(t *testing.T, dev hostgpu.Device) {
	cmds, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Wait(cmds.Commit()); err != nil {
		t.Fatal(err)
	}
	cmds.Barrier()
	if _, err := dev.Begin(); !errors.Is(err, hostgpu.ErrClosed) {
		t.Fatalf("Begin after a command on a committed submission: %v, want %v", err, hostgpu.ErrClosed)
	}
	next, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Wait(next.Commit()); err != nil {
		t.Fatal(err)
	}
}

// HostErrorReported checks that a refused command is reported by the
// Wait on its submission as the host error it is, and that the
// commands after it in the submission are not encoded: a buffer
// filled with one word, then a submission whose copy reaches past the
// buffer's end and whose fill after it would change every word. The
// buffer keeps the first fill, and the device opens the next
// submission as usual.
func HostErrorReported(t *testing.T, dev hostgpu.Device) {
	const words = 64
	buf := newBuffer(t, dev, words*4, hostgpu.BufferWorking)
	src := upload(t, dev, make([]byte, words*4))
	submit(t, dev, func(cmds hostgpu.Commands) { cmds.Fill(buf, 0, words*4, 0x11111111) })
	cmds, err := dev.Begin()
	if err != nil {
		t.Fatal(err)
	}
	cmds.Copy(buf, 0, src, 0, words*8)
	cmds.Fill(buf, 0, words*4, 0x22222222)
	if err := dev.Wait(cmds.Commit()); !errors.Is(err, hostgpu.ErrCopyRange) {
		t.Fatalf("wait on a submission with a refused copy: %v, want %v", err, hostgpu.ErrCopyRange)
	}
	out := ReadBack(t, dev, buf)
	for w := 0; w < words; w++ {
		if got := binary.LittleEndian.Uint32(out[w*4:]); got != 0x11111111 {
			t.Fatalf("word %d = %08x after a refused submission, want 11111111", w, got)
		}
	}
}

// CommandsNoAllocs checks that a submission's encode, commit, and
// wait allocate nothing on the Go side once the first submission has
// run. It covers an index dispatch, a group dispatch, an index grid
// and a fill each past one dispatch, a barrier, and a copy. The
// binding list is built outside the cycle, as the renderer's is. One
// measured run: AllocsPerRun divides the count by the runs, so a run
// of several would hide an allocation made every other cycle.
func CommandsNoAllocs(t *testing.T, dev hostgpu.Device) {
	pipe, rec := wordsPipeline(t, dev)
	buffers, _, _ := wordsBuffers(t, dev)
	probeSize := uint32(unsafe.Sizeof(conformance.Probes()[0]))
	type dispatch struct {
		pipe hostgpu.Pipeline
		rec  hostgpu.Dispatch
		grid [2]int
	}
	var more []dispatch
	for _, e := range conformance.Entries {
		if e.Name != "Group" && e.Name != "Wide" {
			continue
		}
		d := dispatch{pipe: pipeline(t, dev, e.Name), rec: hostgpu.Dispatch{BoundX: e.BoundX, BoundY: e.BoundY, Segment: e.Segment}, grid: e.Grid()}
		if e.Rows > 1 {
			d.rec.ParamsStride = probeSize
		}
		more = append(more, d)
	}
	if len(more) != 2 {
		t.Fatalf("%d of the Group and Wide entries found, want 2", len(more))
	}
	wide := newBuffer(t, dev, fillWideBytes, hostgpu.BufferWorking)
	cycle := func() {
		cmds, err := dev.Begin()
		if err != nil {
			t.Fatal(err)
		}
		cmds.Dispatch(pipe, buffers, rec, [2]int{conformance.Threads, 1})
		cmds.Barrier()
		for _, d := range more {
			cmds.Dispatch(d.pipe, buffers, d.rec, d.grid)
			cmds.Barrier()
		}
		cmds.Copy(buffers[2], 0, buffers[0], 0, 256)
		cmds.Fill(buffers[2], 256, 256, 0)
		cmds.Fill(wide, 0, fillWideBytes, 0)
		if err := dev.Wait(cmds.Commit()); err != nil {
			t.Fatal(err)
		}
	}
	cycle()
	if allocs := testing.AllocsPerRun(1, cycle); allocs != 0 {
		t.Errorf("%.0f allocations per command buffer", allocs)
	}
}
