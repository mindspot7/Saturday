// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

// Operations is the list of operations a renderer implements for the
// compute functions to run on it: the operations a compute function
// cannot write as plain Go, each with a form per renderer. The
// software renderer's forms are the Go bodies in software.go, which
// it runs directly. The GPU renderer's forms are the device forms its
// backend emits into the compute shaders: the compile stage calls the
// backend's method for each operation it meets, so a backend that
// lacks one does not build.
//
// B is what the renderer refers to a buffer by and V what it refers
// to a value by, since a buffer is never a value in the compute
// subset: source text for a backend emitting source, an instruction
// id for one emitting instructions. An operation with no result (the
// writes, Raise, and NextPhase) is a statement, which a backend emits
// where it stands. The compile stage reads the operations from this
// interface: which take a buffer, how many operands each takes, and
// which have a result.
//
// The byte accessors read and write a byte buffer at an address
// masked to the buffer's size, multi-byte accesses big-endian, every
// byte masked individually. Load and Store are a word buffer's index
// and indexed store, the index masked to the buffer's size. Length is
// the length of a variable-length buffer in its own units, bytes or
// words. The Min and Max operations are the Go builtins on two
// operands of the named type. Raise, Add, and Swap are the
// shared-word operations on a word buffer across the threads of a
// dispatch: the atomic maximum with no result, the atomic add
// returning the prior word, and the atomic exchange returning the
// prior word. NextPhase is a group's phase boundary, after which
// every buffer store the group's threads made before it is visible
// to every thread of the group.
//
// This file is outside the embedded set: the interface is the
// renderers' contract, not compute source.
type Operations[B, V any] interface {
	U8(buf B, addr V) V
	U16BE(buf B, addr V) V
	U32BE(buf B, addr V) V
	PutU8(buf B, addr, v V)
	PutU16BE(buf B, addr, v V)
	PutU32BE(buf B, addr, v V)
	Load(buf B, i V) V
	Store(buf B, i, v V)
	Length(buf B) V
	Raise(buf B, i, v V)
	Add(buf B, i, v V) V
	Swap(buf B, i, v V) V
	MinI32(x, y V) V
	MaxI32(x, y V) V
	MinI64(x, y V) V
	MaxI64(x, y V) V
	MinU32(x, y V) V
	MaxU32(x, y V) V
	NextPhase()
}
