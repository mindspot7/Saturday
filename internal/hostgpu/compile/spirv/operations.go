// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// operation evaluates an OpCall node's operands and calls its
// operation on this function, which implements compute.Operations over
// a buffer's local and an instruction id, each operation an inline
// sequence: a buffer operation masks the address with the buffer's
// mask and accesses the block's array, the integer operations are
// GLSL.std.450 instructions, and the phase boundary orders the
// workgroup. An operation with no result yields 0.
func (f *fn) operation(x *compile.OpCall) uint32 {
	var buf *compile.LocalRef
	args := x.Args
	if x.Op.Buffered {
		buf = args[0].(*compile.LocalRef)
		args = args[1:]
	}
	vals := make([]uint32, len(args))
	for i, a := range args {
		vals[i] = f.expr(a)
	}
	return compile.Invoke(f, x.Op, buf, vals)
}

// bufferMask is a buffer's block and address mask: the size constant
// minus one for a fixed buffer, the hidden word count minus one for
// a variable-length view.
func (f *fn) bufferMask(lr *compile.LocalRef) (buf, mask uint32) {
	bt := lr.T.(compile.BufferType)
	if bt.Dynamic {
		return f.bufs[lr.Index], f.result(opISub, f.b.tU32(), f.lens[lr.Index], f.b.constU32(1))
	}
	return f.bufs[lr.Index], f.b.constU32(uint32(bt.Size - 1))
}

// word is the pointer to the word of a word buffer at the masked
// index.
func (f *fn) word(lr *compile.LocalRef, i uint32) uint32 {
	buf, mask := f.bufferMask(lr)
	return f.wordPtr(buf, f.result(opBitwiseAnd, f.b.tU32(), i, mask))
}

// ext is a GLSL.std.450 instruction on two operands of type t.
func (f *fn) ext(inst, t, x, y uint32) uint32 {
	return f.result(opExtInst, t, f.b.glsl, inst, x, y)
}

func (f *fn) U8(lr *compile.LocalRef, a uint32) uint32 {
	buf, mask := f.bufferMask(lr)
	return f.byteLoad(buf, mask, a)
}

func (f *fn) U16BE(lr *compile.LocalRef, a uint32) uint32 {
	b := f.b
	buf, mask := f.bufferMask(lr)
	hi := f.byteLoad(buf, mask, a)
	lo := f.byteLoad(buf, mask, f.addU32(a, 1))
	return f.result(opBitwiseOr, b.tU32(), f.result(opShiftLeftLogical, b.tU32(), hi, b.constU32(8)), lo)
}

func (f *fn) U32BE(lr *compile.LocalRef, a uint32) uint32 {
	b := f.b
	buf, mask := f.bufferMask(lr)
	v := f.byteLoad(buf, mask, a)
	for i := uint32(1); i < 4; i++ {
		v = f.result(opShiftLeftLogical, b.tU32(), v, b.constU32(8))
		v = f.result(opBitwiseOr, b.tU32(), v, f.byteLoad(buf, mask, f.addU32(a, i)))
	}
	return v
}

func (f *fn) PutU8(lr *compile.LocalRef, a, v uint32) {
	buf, mask := f.bufferMask(lr)
	f.byteStore(buf, mask, a, v)
}

func (f *fn) PutU16BE(lr *compile.LocalRef, a, v uint32) {
	b := f.b
	buf, mask := f.bufferMask(lr)
	f.byteStore(buf, mask, a, f.result(opShiftRightLogical, b.tU32(), v, b.constU32(8)))
	f.byteStore(buf, mask, f.addU32(a, 1), v)
}

func (f *fn) PutU32BE(lr *compile.LocalRef, a, v uint32) {
	b := f.b
	buf, mask := f.bufferMask(lr)
	for i := uint32(0); i < 4; i++ {
		f.byteStore(buf, mask, f.addU32(a, i), f.result(opShiftRightLogical, b.tU32(), v, b.constU32(24-8*i)))
	}
}

func (f *fn) Load(lr *compile.LocalRef, i uint32) uint32 {
	return f.bufferLoad(f.b.tU32(), f.word(lr, i))
}

func (f *fn) Store(lr *compile.LocalRef, i, v uint32) { f.bufferStore(f.word(lr, i), v) }

func (f *fn) Length(lr *compile.LocalRef) uint32 { return f.lens[lr.Index] }

func (f *fn) Raise(lr *compile.LocalRef, i, v uint32) {
	b := f.b
	f.result(opAtomicUMax, b.tU32(), f.word(lr, i), b.constU32(scopeDevice), b.constU32(semanticsRelaxed), v)
}

func (f *fn) Add(lr *compile.LocalRef, i, v uint32) uint32 {
	b := f.b
	return f.result(opAtomicIAdd, b.tU32(), f.word(lr, i), b.constU32(scopeDevice), b.constU32(semanticsRelaxed), v)
}

func (f *fn) Swap(lr *compile.LocalRef, i, v uint32) uint32 {
	b := f.b
	return f.result(opAtomicExchange, b.tU32(), f.word(lr, i), b.constU32(scopeDevice), b.constU32(semanticsRelaxed), v)
}

func (f *fn) MinI32(x, y uint32) uint32 { return f.ext(glslSMin, f.b.tI32(), x, y) }

func (f *fn) MaxI32(x, y uint32) uint32 { return f.ext(glslSMax, f.b.tI32(), x, y) }

func (f *fn) MinI64(x, y uint32) uint32 { return f.ext(glslSMin, f.b.tI64(), x, y) }

func (f *fn) MaxI64(x, y uint32) uint32 { return f.ext(glslSMax, f.b.tI64(), x, y) }

func (f *fn) MinU32(x, y uint32) uint32 { return f.ext(glslUMin, f.b.tU32(), x, y) }

func (f *fn) MaxU32(x, y uint32) uint32 { return f.ext(glslUMax, f.b.tU32(), x, y) }

// NextPhase is a control barrier over the workgroup, the threads the
// phase boundary orders, under acquire and release semantics over
// uniform memory with the availability and visibility operations: the
// workgroup's non-private storage buffer writes before it are
// available to and visible to its non-private reads after it. The
// memory scope is the workgroup, since only the group's own threads
// read what the phase wrote; a later dispatch reads it after the
// device's barrier between them.
func (f *fn) NextPhase() {
	b := f.b
	const semantics = semanticsAcquireRelease | semanticsUniformMemory | semanticsMakeAvailable | semanticsMakeVisible
	f.emit(opControlBarrier, b.constU32(scopeWorkgroup), b.constU32(scopeWorkgroup), b.constU32(semantics))
}

func (f *fn) addU32(a uint32, n uint32) uint32 {
	return f.result(opIAdd, f.b.tU32(), a, f.b.constU32(n))
}

// bytePtr is the pointer to the masked byte of a byte block.
func (f *fn) bytePtr(buf, mask, a uint32) uint32 {
	b := f.b
	idx := f.result(opBitwiseAnd, b.tU32(), a, mask)
	return f.result(opAccessChain, b.ptr(storageStorageBuffer, b.tU8()), buf, b.constU32(0), idx)
}

// byteLoad reads the masked byte as a word.
func (f *fn) byteLoad(buf, mask, a uint32) uint32 {
	return f.result(opUConvert, f.b.tU32(), f.bufferLoad(f.b.tU8(), f.bytePtr(buf, mask, a)))
}

// byteStore writes the low byte of a word at the masked address.
func (f *fn) byteStore(buf, mask, a, v uint32) {
	f.bufferStore(f.bytePtr(buf, mask, a), f.result(opUConvert, f.b.tU8(), v))
}

// wordPtr is the pointer to a word of a word block by index.
func (f *fn) wordPtr(buf, idx uint32) uint32 {
	b := f.b
	return f.result(opAccessChain, b.ptr(storageStorageBuffer, b.tU32()), buf, b.constU32(0), idx)
}

// loadStruct gathers a params row from the params word block at a
// byte address (loadAt). The row's address is a multiple of four by
// the packet's construction.
func (f *fn) loadStruct(x *compile.LoadStruct) uint32 {
	return f.loadStructAt(x, f.expr(x.Addr))
}

// loadStructAt gathers the row at the byte address a, x's address
// already evaluated.
func (f *fn) loadStructAt(x *compile.LoadStruct, a uint32) uint32 {
	return f.loadAt(f.bufs[x.Buffer.(*compile.LocalRef).Index], x.T, a)
}

// size is a buffer-visible type's size in bytes by the layout rule.
func size(m *compile.Module, t compile.Type) int {
	switch u := t.(type) {
	case compile.Scalar:
		return u.Bytes()
	case compile.ArrayType:
		return size(m, u.Elem) * u.Len
	case compile.StructRef:
		return m.Structs[u.Index].Size
	}
	return 0
}
