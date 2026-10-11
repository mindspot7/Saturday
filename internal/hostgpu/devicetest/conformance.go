// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package devicetest

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// Conformance runs every conformance and buffer test on the device,
// each a subtest.
func Conformance(t *testing.T, dev hostgpu.Device) {
	t.Run("Entries", func(t *testing.T) { entries(t, dev) })
	t.Run("RoundState", func(t *testing.T) { roundState(t, dev) })
	t.Run("VariableBuffers", func(t *testing.T) { variableBuffers(t, dev) })
	t.Run("VariableByteBuffers", func(t *testing.T) { variableByteBuffers(t, dev) })
	t.Run("VariableArray", func(t *testing.T) { variableArray(t, dev) })
	t.Run("VariableHalfwords", func(t *testing.T) { variableHalfwords(t, dev) })
	t.Run("DeviceBuffers", func(t *testing.T) { deviceBuffers(t, dev) })
	t.Run("DispatchOrder", func(t *testing.T) { dispatchOrder(t, dev) })
	t.Run("SubmissionOrder", func(t *testing.T) { submissionOrder(t, dev) })
	t.Run("OutputBuffers", func(t *testing.T) { outputBuffers(t, dev) })
	t.Run("Copy", func(t *testing.T) { copies(t, dev) })
	t.Run("Fill", func(t *testing.T) { fills(t, dev) })
}

// entries compares every conformance entry's output words and byte
// buffer against the Go execution, reporting the first differing
// word per entry. Each entry is a subtest that runs in one
// submission: the byte buffer set from its upload and the output
// cleared, the dispatch, and both copied out. The params rows sit
// behind a leading copy of row 1 and every dispatch names them by
// ParamsOffset, so a backend that drops the offset reads row 1 where
// row 0 belongs.
func entries(t *testing.T, dev hostgpu.Device) {
	in := make(compute.Layer, conformance.LayerWords)
	mem := new(compute.VDP2VRAM)
	conformance.Inputs(in, mem)
	probes := conformance.Probes()
	probeSize := uint32(unsafe.Sizeof(probes[0]))

	inBuf := upload(t, dev, wordBytes(in))
	memUp := upload(t, dev, mem[:])
	lead := probes[1]
	probeBuf := upload(t, dev, append(bytes.Clone(valueBytes(&lead)), valueBytes(&probes)...))
	memBuf := newBuffer(t, dev, len(mem), hostgpu.BufferWorking)
	outBuf := newBuffer(t, dev, conformance.LayerWords*4, hostgpu.BufferWorking)
	memBack := newBuffer(t, dev, len(mem), hostgpu.BufferReadback)
	outBack := newBuffer(t, dev, conformance.LayerWords*4, hostgpu.BufferReadback)

	for _, e := range conformance.Entries {
		t.Run(e.Name, func(t *testing.T) {
			pipe := pipeline(t, dev, e.Name)
			goMem := *mem
			goOut := make(compute.Layer, conformance.LayerWords)
			conformance.RunGo(e, in, &goMem, goOut, &probes)

			rec := hostgpu.Dispatch{BoundX: e.BoundX, BoundY: e.BoundY, ParamsOffset: probeSize, Segment: e.Segment}
			if e.Rows > 1 {
				rec.ParamsStride = probeSize
			}
			submit(t, dev, func(cmds hostgpu.Commands) {
				cmds.Copy(memBuf, 0, memUp, 0, len(mem))
				cmds.Fill(outBuf, 0, outBuf.Size(), 0)
				cmds.Barrier()
				cmds.Dispatch(pipe, []hostgpu.Buffer{inBuf, memBuf, outBuf, probeBuf}, rec, e.Grid())
				cmds.Barrier()
				cmds.Copy(memBack, 0, memBuf, 0, len(mem))
				cmds.Copy(outBack, 0, outBuf, 0, outBuf.Size())
			})

			gpuOut := unsafe.Slice((*uint32)(unsafe.Pointer(&outBack.Bytes()[0])), len(goOut))
			for w := range goOut {
				if gpuOut[w] != goOut[w] {
					t.Errorf("word %d (thread %d word %d): gpu %08x go %08x (inputs %08x %08x)", w, w/8, w%8, gpuOut[w], goOut[w], in[(w/8)*8], in[(w/8)*8+1])
					break
				}
			}
			if got := memBack.Bytes(); !bytes.Equal(got, goMem[:]) {
				for a := range goMem {
					if got[a] != goMem[a] {
						t.Errorf("byte buffer differs at %d: gpu %02x go %02x", a, got[a], goMem[a])
						break
					}
				}
			}
		})
	}
}

// roundState dispatches each RoundState entry's groups across several
// workgroups over zeroed regions, at the test's width and at the
// width the device sizes for a load (GroupThreads), and holds the
// result against the Go execution through CheckRoundState.
func roundState(t *testing.T, dev hostgpu.Device) {
	probes := conformance.Probes()
	var regions, backs [3]hostgpu.Buffer
	for i := range regions {
		regions[i] = newBuffer(t, dev, conformance.RoundStateWords*4, hostgpu.BufferWorking)
		backs[i] = newBuffer(t, dev, conformance.RoundStateWords*4, hostgpu.BufferReadback)
	}
	params := upload(t, dev, valueBytes(&probes))
	// A load whose largest op has more chunks than the test's width,
	// so the device's width differs from it on every sizing rule.
	sized := dev.GroupThreads(hostgpu.GroupLoad{OpCount: conformance.RoundStateGroups, MaxChunks: 3000, SumChunks: 3100})
	if sized <= 0 {
		t.Fatalf("GroupThreads sized a row at %d threads", sized)
	}
	for _, name := range conformance.RoundStateEntries {
		pipe := pipeline(t, dev, name)
		want := conformance.NewRoundState()
		if err := conformance.RunRoundStateGo(name, want); err != nil {
			t.Fatal(err)
		}
		for _, threads := range []int{conformance.RoundStateThreads, sized} {
			rec := hostgpu.Dispatch{BoundX: uint32(threads), BoundY: conformance.RoundStateGroups, ParamsStride: uint32(unsafe.Sizeof(probes[0]))}
			submit(t, dev, func(cmds hostgpu.Commands) {
				for _, r := range regions {
					cmds.Fill(r, 0, r.Size(), 0)
				}
				cmds.Barrier()
				cmds.Dispatch(pipe, []hostgpu.Buffer{regions[0], regions[1], regions[2], params}, rec, [2]int{threads, conformance.RoundStateGroups})
				cmds.Barrier()
				for i, r := range regions {
					cmds.Copy(backs[i], 0, r, 0, r.Size())
				}
			})
			got := conformance.RoundStateOf(backs[0].Bytes(), backs[1].Bytes(), backs[2].Bytes())
			if err := conformance.CheckRoundState(name, got, want, uint32(threads)); err != nil {
				t.Errorf("%d threads: %v", threads, err)
			}
		}
	}
}

// variableBuffers holds the variable-buffer entry against the Go
// execution at every length, and the submission that sets the output,
// dispatches, and copies it out at zero allocations.
func variableBuffers(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableEntry)
	for _, n := range conformance.VariableSizes {
		in, out := conformance.VariableInputs(n)
		p := conformance.Probes()[0]
		inBuf := upload(t, dev, wordBytes(in))
		outUp := upload(t, dev, wordBytes(out))
		params := upload(t, dev, valueBytes(&p))
		outBuf := newBuffer(t, dev, len(out)*4, hostgpu.BufferWorking)
		outBack := newBuffer(t, dev, len(out)*4, hostgpu.BufferReadback)
		conformance.RunVariableGo(in, out)
		bindings := []hostgpu.Buffer{inBuf, outBuf, params}
		cycle := func() {
			cmds, err := dev.Begin()
			if err != nil {
				t.Fatal(err)
			}
			cmds.Copy(outBuf, 0, outUp, 0, outUp.Size())
			cmds.Barrier()
			cmds.Dispatch(pipe, bindings, hostgpu.Dispatch{BoundX: uint32(len(out)), BoundY: 1}, [2]int{len(out), 1})
			cmds.Barrier()
			cmds.Copy(outBack, 0, outBuf, 0, outBuf.Size())
			if err := dev.Wait(cmds.Commit()); err != nil {
				t.Fatal(err)
			}
		}
		cycle()
		if !bytes.Equal(outBack.Bytes(), wordBytes(out)) {
			t.Fatalf("variable buffer length %d differs from Go", n)
		}
		if n == conformance.VariableSizes[0] {
			if allocs := testing.AllocsPerRun(1, cycle); allocs != 0 {
				t.Fatalf("variable dispatch allocated %g times", allocs)
			}
		}
	}
}

// variableByteBuffers holds the variable byte buffer entry against
// the Go execution at every length.
func variableByteBuffers(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableByteEntry)
	for _, n := range conformance.VariableSizes {
		in, out := conformance.VariableByteInputs(n)
		p := conformance.Probes()[0]
		got := runToOut(t, dev, pipe, []hostgpu.Buffer{upload(t, dev, in)}, out, upload(t, dev, valueBytes(&p)), len(out))
		conformance.RunVariableBytesGo(in, out)
		if !bytes.Equal(got, out) {
			t.Fatalf("variable byte buffer length %d differs from Go", n)
		}
	}
}

// variableArray holds the array of variable buffers entry's
// variable-index read against the Go execution at every length.
func variableArray(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableArrayEntry)
	for _, n := range conformance.VariableSizes {
		v, out := conformance.VariableArrayInputs(n)
		p := conformance.Probes()[0]
		inputs := []hostgpu.Buffer{upload(t, dev, wordBytes(v.In[0])), upload(t, dev, wordBytes(v.In[1]))}
		got := runToOut(t, dev, pipe, inputs, wordBytes(out), upload(t, dev, valueBytes(&p)), len(out))
		conformance.RunVariableArrayGo(&v, out)
		if !bytes.Equal(got, wordBytes(out)) {
			t.Fatalf("variable array length %d differs from Go", n)
		}
	}
}

// variableHalfwords holds the variable halfword entry's straddling
// 16-bit accesses against the Go execution at every length.
func variableHalfwords(t *testing.T, dev hostgpu.Device) {
	pipe := pipeline(t, dev, conformance.VariableHalfwordEntry)
	for _, n := range conformance.VariableSizes {
		in, out := conformance.VariableHalfwordInputs(n)
		p := conformance.Probes()[0]
		got := runToOut(t, dev, pipe, []hostgpu.Buffer{upload(t, dev, in)}, out, upload(t, dev, valueBytes(&p)), len(out)/2)
		conformance.RunVariableHalfwordsGo(in, out)
		if !bytes.Equal(got, out) {
			t.Fatalf("variable halfword length %d differs from Go", n)
		}
	}
}
