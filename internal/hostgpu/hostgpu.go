// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package hostgpu is the host side of the GPU renderer: it takes
// the compute packages' source, compiles it to the compute IR, and has
// the platform's backend lower the IR to its compute shaders and
// build them into pipelines. The renderer names nothing beyond the
// types here. A backend (the metal package on macOS, the dx12 package
// on Windows, the vulkan package on Linux and on Windows under the
// vulkan build tag) implements Device, Buffer, and Commands; the
// backend package opens the one the platform has.
package hostgpu

import (
	"errors"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// ErrUnavailable is the open error on a platform without a backend,
// or one whose API cannot be initialized. The renderer takes it as
// the signal to use the software path.
var ErrUnavailable = errors.New("no GPU backend available")

// HostError is a request the device contract refuses: a programming
// error in the caller, never a device failure. A backend checks a
// request before it records anything, latches the first host error
// of a submission on its encoder, encodes none of the submission's
// later commands, and reports the error from the Wait on that
// submission, joined with any device error. A command encoded after
// Commit latches ErrClosed on the closed encoder, which the next
// Begin reports. The values are constants, so reporting one allocates
// nothing on the frame path.
type HostError string

func (e HostError) Error() string { return string(e) }

const (
	ErrClosed        HostError = "encoding on a committed submission"
	ErrForeign       HostError = "a pipeline or buffer of another backend"
	ErrBindingCount  HostError = "dispatch binds a buffer count the entry does not declare"
	ErrReadbackBound HostError = "a readback buffer bound to a dispatch"
	ErrVariableWords HostError = "a variable-length buffer's word count is not a power of two"
	ErrGrid          HostError = "a grid dimension is negative or its workgroups exceed 32 bits"
	ErrGroupWide     HostError = "a group entry's grid is wider than one dispatch"
	ErrGridRows      HostError = "a grid has more rows than one dispatch issues"
	ErrCopySame      HostError = "copy within one buffer"
	ErrCopyUpload    HostError = "copy into an upload buffer"
	ErrCopyReadback  HostError = "copy from a readback buffer"
	ErrCopyAlign     HostError = "copy offset or length not a non-negative multiple of four"
	ErrCopyRange     HostError = "copy past the end of a buffer"
	ErrFillKind      HostError = "fill of a buffer other than a working or an output buffer"
	ErrFillAlign     HostError = "fill offset or length not a non-negative multiple of four"
	ErrFillRange     HostError = "fill past the end of the buffer"
)

// Source is one compute package as the compile stage takes it: its
// import path, the file system holding its embedded Go source, and
// the names of its entry points.
type Source = compile.Source

// Module is the compute IR a backend builds: the lowered compute
// functions and their entry points.
type Module = compile.Module

// Device is an opened backend: it builds a lowered module into one
// pipeline per entry point, in the module's entry order, allocates
// buffers of a kind, encodes and waits on submissions, and closes.
type Device interface {
	// Name is the device's name. The backend is the backend
	// package's Name, which a caller adds where it displays one.
	Name() string
	Build(mod *Module) ([]Pipeline, error)
	// NewBuffer allocates a buffer of size bytes, placed in the memory
	// the backend chooses for the kind.
	NewBuffer(size int, kind BufferKind) (Buffer, error)
	// GroupThreads is the threads across each row of a group dispatch
	// of the load, the grid width the caller passes to Dispatch. A
	// group entry's result is the same for any count, so the backend
	// sizes it for its device (GroupWidth).
	GroupThreads(load GroupLoad) int
	// Begin starts encoding a submission. One submission is encoded
	// at a time: a Begin while another is being encoded is an error,
	// and so is a command encoded after the previous Commit, which
	// Begin reports as ErrClosed. A submission's commands follow every
	// earlier submission's writes, as if a Barrier stood between them.
	Begin() (Commands, error)
	// Wait blocks until the submission completes and reports a GPU
	// error and the submission's host error, if its encoding latched
	// one. Submissions complete in the order committed, so a Wait
	// also releases every earlier submission not waited on and
	// reports its errors. A submission already released reports
	// nothing; one not yet committed is an error.
	Wait(s Submission) error
	// Close waits for every submission still in flight and releases
	// the pipelines and the device. Buffers are released by their
	// owner before it, once the owner has waited on every submission
	// that binds them. A submission still being encoded is abandoned,
	// not submitted. The device is not used after Close.
	Close()
}

// Submission names a committed submission: the device's sequence
// number, one per Commit, never reused, so a Submission held past
// the wait that released it still names the submission it was
// given for. Zero names none.
type Submission uint64

// Pipeline is a compute pipeline for one entry point, opaque to the
// renderer: the backend that built it is the only one that reads it,
// when the renderer hands it back to Dispatch. The pipeline carries
// the workgroup size its backend chose for the entry.
type Pipeline interface{}

// BufferKind names the caller's access to a buffer and no memory: the
// backend chooses where each kind lives, and may choose differently
// for one device than for another.
type BufferKind uint8

const (
	// BufferUpload: the CPU writes, the GPU reads, through a
	// dispatch binding or as a Copy source. The CPU may read its own
	// writes, and a backend may place it where such reads are slow.
	BufferUpload BufferKind = iota
	// BufferReadback: the GPU writes it only as a Copy destination,
	// and the CPU reads it after the Wait on the submission that
	// wrote it. A backend may place it where only a copy can write.
	BufferReadback
	// BufferWorking: the render's working data. The caller never
	// reads or writes its bytes and Bytes is nil: only dispatches,
	// Copy, and Fill touch it. Whether its contents are rebuilt each
	// frame or held across frames is the caller's and does not change
	// the kind.
	BufferWorking
	// BufferOutput: the GPU writes it through dispatches, Copy, and
	// Fill, and the CPU reads it after the Wait on the submission
	// that wrote it, and never writes it. A backend sharing memory
	// between the CPU and the GPU places it where the dispatches
	// write and the CPU reads the same bytes; a discrete device places
	// it where the GPU's writes reach the CPU, by the placement its
	// measurements favor.
	BufferOutput
)

// Buffer is a buffer of one kind. An upload, readback, or output
// buffer's Bytes is its memory, which the CPU reads and writes
// directly as its kind allows. A submission sees the CPU's writes
// made before its Commit, and the CPU sees a submission's writes
// after the Wait on it. The CPU does not write a buffer a submission
// in flight reads. A working buffer's Bytes is nil. Size is the byte
// size the buffer was allocated with, for every kind.
type Buffer interface {
	Bytes() []byte
	Size() int
	Kind() BufferKind
	Release()
}

// Dispatch is the record every dispatch delivers to its entry point's
// wrapper: the bounds the wrapper guards, where the params rows start
// and their stride, and a span entry's segment. It is a fixed 20-byte
// block of five words, the layout the backends' emitted dispatch
// struct declares ahead of any words a backend adds for itself.
//
// A span entry's Segment is the unit one thread takes whole, and the
// grid across is the units over BoundX. A backend may give each thread
// a whole number k of units: it dispatches the record with Segment
// times k and the grid across as BoundX over the new segment, rounded
// up. The span contract makes the result the same for any segment, so
// k is the backend's, chosen from the entry's form and the unit alone,
// never from the entry's name.
type Dispatch struct {
	BoundX       uint32
	BoundY       uint32
	ParamsOffset uint32
	ParamsStride uint32
	Segment      uint32
}

// Commands is the submission being encoded. Dispatch binds the
// buffers at indices 0 to n-1 in order and the record after them, and
// runs the pipeline over the grid: grid[0] threads across and grid[1]
// rows, covered by whole workgroups of the pipeline's size. A backend
// covers an index or span grid of any width, and the renderer never
// sees how. A group entry's grid is a row per group, its threads
// across rounded up to whole workgroups of the backend's size, so a
// group one across is one workgroup on every backend. A group's
// threads are never split across dispatches, so a group grid wider
// than one dispatch of the backend issues is a host error
// (ErrGroupWide), and so is a grid of any form with more rows than
// one dispatch issues (ErrGridRows), since rows are never split
// either.
//
// For each variable-length binding the backend supplies the buffer's
// word count (Lengths). The helpers mask with it, so the software
// renderer's view of the buffer has the same length. No slice header
// crosses the device ABI.
//
// Barrier orders the commands before it ahead of those after. Commit
// submits and returns the Submission the device waits on. The
// Commands is then finished, and the next Begin starts another.
//
// A host error in any command (HostError) is latched: the command is
// not recorded, nothing later in the submission is, and the Wait on
// the submission reports it. The methods return nothing, so the
// caller encodes a submission as a whole and reads one result.
//
// Copy copies n bytes from src at srcOffset to dst at dstOffset, two
// distinct buffers of any kinds, the offsets and n multiples of four
// and the ranges inside the buffers (CheckCopy). Fill writes value to
// every word of n bytes of dst from offset, the offset and n
// multiples of four and the range inside the buffer (CheckFill).
// Barrier orders both as it does a dispatch.
type Commands interface {
	Dispatch(p Pipeline, buffers []Buffer, rec Dispatch, grid [2]int)
	Copy(dst Buffer, dstOffset int, src Buffer, srcOffset int, n int)
	Fill(dst Buffer, offset int, n int, value uint32)
	Barrier()
	Commit() Submission
}
