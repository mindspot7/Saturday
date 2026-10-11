// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"fmt"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// SPIR-V opcodes, enumerants, and the GLSL.std.450 instructions used
// here, from the SPIR-V 1.5 specification and the extended
// instruction set specification.
const (
	opName                = 5
	opExtension           = 10
	opExtInstImport       = 11
	opExtInst             = 12
	opMemoryModel         = 14
	opEntryPoint          = 15
	opExecutionMode       = 16
	opCapability          = 17
	opTypeVoid            = 19
	opTypeBool            = 20
	opTypeInt             = 21
	opTypeFloat           = 22
	opTypeVector          = 23
	opTypeArray           = 28
	opTypeRuntimeArray    = 29
	opTypeStruct          = 30
	opTypePointer         = 32
	opTypeFunction        = 33
	opConstantTrue        = 41
	opConstantFalse       = 42
	opConstant            = 43
	opConstantComposite   = 44
	opConstantNull        = 46
	opFunction            = 54
	opFunctionParameter   = 55
	opFunctionEnd         = 56
	opFunctionCall        = 57
	opVariable            = 59
	opLoad                = 61
	opStore               = 62
	opAccessChain         = 65
	opDecorate            = 71
	opMemberDecorate      = 72
	opCompositeConstruct  = 80
	opCompositeExtract    = 81
	opConvertFToU         = 109
	opConvertFToS         = 110
	opConvertSToF         = 111
	opConvertUToF         = 112
	opUConvert            = 113
	opSConvert            = 114
	opBitcast             = 124
	opSNegate             = 126
	opFNegate             = 127
	opIAdd                = 128
	opFAdd                = 129
	opISub                = 130
	opFSub                = 131
	opIMul                = 132
	opFMul                = 133
	opUDiv                = 134
	opSDiv                = 135
	opFDiv                = 136
	opUMod                = 137
	opSRem                = 138
	opFRem                = 140
	opLogicalEqual        = 164
	opLogicalNotEqual     = 165
	opLogicalOr           = 166
	opLogicalAnd          = 167
	opLogicalNot          = 168
	opSelect              = 169
	opIEqual              = 170
	opINotEqual           = 171
	opUGreaterThan        = 172
	opSGreaterThan        = 173
	opUGreaterThanEqual   = 174
	opSGreaterThanEqual   = 175
	opULessThan           = 176
	opSLessThan           = 177
	opULessThanEqual      = 178
	opSLessThanEqual      = 179
	opFOrdEqual           = 180
	opFUnordNotEqual      = 183
	opFOrdLessThan        = 184
	opFOrdGreaterThan     = 186
	opFOrdLessThanEqual   = 188
	opFOrdGreaterThanEqua = 190
	opShiftRightLogical   = 194
	opShiftRightArith     = 195
	opShiftLeftLogical    = 196
	opBitwiseOr           = 197
	opBitwiseXor          = 198
	opBitwiseAnd          = 199
	opNot                 = 200
	opControlBarrier      = 224
	opAtomicExchange      = 229
	opAtomicIAdd          = 234
	opAtomicUMax          = 239
	opPhi                 = 245
	opLoopMerge           = 246
	opSelectionMerge      = 247
	opLabel               = 248
	opBranch              = 249
	opBranchConditional   = 250
	opSwitch              = 251
	opReturn              = 253
	opReturnValue         = 254
	opUnreachable         = 255

	capShader                        = 1
	capInt64                         = 11
	capVariablePointersStorageBuffer = 4441
	capStorageBuffer8BitAccess       = 4448
	capVulkanMemoryModel             = 5345
	capVulkanMemoryModelDeviceScope  = 5346

	executionModelGLCompute = 5
	executionModeLocalSize  = 17
	addressingLogical       = 0
	memoryVulkan            = 3

	memoryOperandNonPrivatePointer = 0x20

	storageInput         = 1
	storagePrivate       = 6
	storageFunction      = 7
	storagePushConstant  = 9
	storageStorageBuffer = 12

	decorationBlock         = 2
	decorationArrayStride   = 6
	decorationBuiltIn       = 11
	decorationNonWritable   = 24
	decorationBinding       = 33
	decorationDescriptorSet = 34
	decorationOffset        = 35

	builtInNumWorkgroups      = 24
	builtInWorkgroupId        = 26
	builtInLocalInvocationId  = 27
	builtInGlobalInvocationId = 28

	scopeDevice    = 1
	scopeWorkgroup = 2

	semanticsRelaxed        = 0
	semanticsAcquireRelease = 0x8
	semanticsUniformMemory  = 0x40
	semanticsMakeAvailable  = 0x2000
	semanticsMakeVisible    = 0x4000

	glslUMin = 38
	glslSMin = 39
	glslUMax = 41
	glslSMax = 42
)

// magic and version are the module header's first two words: the
// SPIR-V magic number and version 1.5, the first with the Vulkan
// memory model and 8-bit storage in core, which a Vulkan 1.2 or
// later device accepts.
const (
	magic   = 0x07230203
	version = 0x00010500
)

// op appends one instruction to a section: the word count in the
// high half of the first word, the opcode in the low half, then the
// operands.
func op(dst *[]uint32, opcode uint32, operands ...uint32) {
	*dst = append(*dst, uint32(len(operands)+1)<<16|opcode)
	*dst = append(*dst, operands...)
}

// words encodes a string as a literal: the UTF-8 bytes packed little
// endian into words with at least one terminating zero byte.
func words(s string) []uint32 {
	b := append([]byte(s), 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	w := make([]uint32, len(b)/4)
	for i := range w {
		w[i] = uint32(b[4*i]) | uint32(b[4*i+1])<<8 | uint32(b[4*i+2])<<16 | uint32(b[4*i+3])<<24
	}
	return w
}

// builder is one module's word stream and id space: the sections in
// the order the module lays them out, the ids handed out, and the
// interned types and constants. Interning keys are strings built
// from the operands, and every intern appends on first use, so two
// runs over the same IR produce the same words.
type builder struct {
	bound uint32

	caps, exts, imports, memory, entry, modes, debug, decor, types, funcs []uint32

	typeIDs  map[string]uint32
	constIDs map[string]uint32
	glsl     uint32   // the GLSL.std.450 import
	globals  []uint32 // every module-scope variable, in declaration order (global)

	// laidOut is every type given a layout decoration, an ArrayStride
	// or member Offsets, which Vulkan forbids in the Function and
	// Private storage classes; a pointer of those classes to one is
	// refused.
	laidOut map[uint32]bool

	err error
}

func newBuilder() *builder {
	b := &builder{typeIDs: map[string]uint32{}, constIDs: map[string]uint32{}, laidOut: map[uint32]bool{}}
	b.glsl = b.id()
	op(&b.imports, opExtInstImport, append([]uint32{b.glsl}, words("GLSL.std.450")...)...)
	return b
}

// id allocates the next result id.
func (b *builder) id() uint32 {
	b.bound++
	return b.bound
}

func (b *builder) fail(format string, args ...any) {
	if b.err == nil {
		b.err = fmt.Errorf(format, args...)
	}
}

// typ interns a type declared by opcode and operands under key.
func (b *builder) typ(key string, opcode uint32, operands ...uint32) uint32 {
	if id, ok := b.typeIDs[key]; ok {
		return id
	}
	id := b.id()
	b.typeIDs[key] = id
	op(&b.types, opcode, append([]uint32{id}, operands...)...)
	return id
}

func (b *builder) tVoid() uint32  { return b.typ("void", opTypeVoid) }
func (b *builder) tBool() uint32  { return b.typ("bool", opTypeBool) }
func (b *builder) tI32() uint32   { return b.typ("i32", opTypeInt, 32, 1) }
func (b *builder) tU32() uint32   { return b.typ("u32", opTypeInt, 32, 0) }
func (b *builder) tI64() uint32   { return b.typ("i64", opTypeInt, 64, 1) }
func (b *builder) tU64() uint32   { return b.typ("u64", opTypeInt, 64, 0) }
func (b *builder) tU8() uint32    { return b.typ("u8", opTypeInt, 8, 0) }
func (b *builder) tF32() uint32   { return b.typ("f32", opTypeFloat, 32) }
func (b *builder) tUVec3() uint32 { return b.typ("uvec3", opTypeVector, b.tU32(), 3) }

// scalar is the register type of a scalar: narrow integers ride in
// the 32-bit type of their signedness.
func (b *builder) scalar(s compile.Scalar) uint32 {
	switch compile.Kind(s) {
	case compile.Bool:
		return b.tBool()
	case compile.I8, compile.I16, compile.I32:
		return b.tI32()
	case compile.U8, compile.U16, compile.U32:
		return b.tU32()
	case compile.I64:
		return b.tI64()
	case compile.U64:
		return b.tU64()
	case compile.F32:
		return b.tF32()
	}
	b.fail("no register type for %s", s)
	return b.tU32()
}

// array interns an undecorated fixed array, the form locals and
// tables take.
func (b *builder) array(elem uint32, n int) uint32 {
	return b.typ(fmt.Sprintf("array:%d:%d", elem, n), opTypeArray, elem, b.constU32(uint32(n)))
}

// strct interns an undecorated struct by its member types.
func (b *builder) strct(members []uint32) uint32 {
	return b.typ(fmt.Sprintf("struct:%v", members), opTypeStruct, members...)
}

// ptr interns a pointer type. A Function or Private pointer to a
// laid-out type is an error: Vulkan forbids explicit layout in those
// classes, and the emitter never asks for one.
func (b *builder) ptr(storage uint32, t uint32) uint32 {
	if (storage == storageFunction || storage == storagePrivate) && b.laidOut[t] {
		b.fail("pointer of storage class %d to a laid-out type", storage)
	}
	return b.typ(fmt.Sprintf("ptr:%d:%d", storage, t), opTypePointer, storage, t)
}

// fnType interns a function type.
func (b *builder) fnType(ret uint32, params []uint32) uint32 {
	return b.typ(fmt.Sprintf("fn:%d:%v", ret, params), opTypeFunction, append([]uint32{ret}, params...)...)
}

// block interns a storage buffer block: a struct holding one runtime
// array of the element type with the stride, decorated Block with
// the array at offset zero. The array and the struct are laid out,
// so neither is ever the type of a local.
func (b *builder) block(elem uint32, stride uint32) uint32 {
	key := fmt.Sprintf("block:%d:%d", elem, stride)
	if id, ok := b.typeIDs[key]; ok {
		return id
	}
	arr := b.id()
	op(&b.types, opTypeRuntimeArray, arr, elem)
	op(&b.decor, opDecorate, arr, decorationArrayStride, stride)
	b.laidOut[arr] = true
	id := b.id()
	b.typeIDs[key] = id
	op(&b.types, opTypeStruct, id, arr)
	op(&b.decor, opDecorate, id, decorationBlock)
	op(&b.decor, opMemberDecorate, id, 0, decorationOffset, 0)
	b.laidOut[id] = true
	return id
}

// constant interns a constant of a type by its literal words.
func (b *builder) constant(t uint32, lit ...uint32) uint32 {
	key := fmt.Sprintf("const:%d:%v", t, lit)
	if id, ok := b.constIDs[key]; ok {
		return id
	}
	id := b.id()
	b.constIDs[key] = id
	op(&b.types, opConstant, append([]uint32{t, id}, lit...)...)
	return id
}

func (b *builder) constU32(v uint32) uint32 { return b.constant(b.tU32(), v) }
func (b *builder) constI32(v int32) uint32  { return b.constant(b.tI32(), uint32(v)) }
func (b *builder) constU64(v uint64) uint32 { return b.constant(b.tU64(), uint32(v), uint32(v>>32)) }
func (b *builder) constI64(v int64) uint32 {
	return b.constant(b.tI64(), uint32(v), uint32(uint64(v)>>32))
}

func (b *builder) constBool(v bool) uint32 {
	key := "false"
	opcode := uint32(opConstantFalse)
	if v {
		key, opcode = "true", opConstantTrue
	}
	if id, ok := b.constIDs[key]; ok {
		return id
	}
	id := b.id()
	b.constIDs[key] = id
	op(&b.types, opcode, b.tBool(), id)
	return id
}

// null interns the null constant of a type.
func (b *builder) null(t uint32) uint32 {
	key := fmt.Sprintf("null:%d", t)
	if id, ok := b.constIDs[key]; ok {
		return id
	}
	id := b.id()
	b.constIDs[key] = id
	op(&b.types, opConstantNull, t, id)
	return id
}

// composite interns a constant composite of a type from constant
// element ids.
func (b *builder) composite(t uint32, elems []uint32) uint32 {
	key := fmt.Sprintf("composite:%d:%v", t, elems)
	if id, ok := b.constIDs[key]; ok {
		return id
	}
	id := b.id()
	b.constIDs[key] = id
	op(&b.types, opConstantComposite, append([]uint32{t, id}, elems...)...)
	return id
}

// global declares a module-level variable of the pointer type in the
// storage class, with an initializer when one is given, and records
// it for the entry point's interface: from SPIR-V 1.4 the interface
// lists every module-scope variable the entry's call tree references,
// of every storage class, and every variable declared here is one the
// entry references, each being declared on first use or for the shell.
func (b *builder) global(ptrType uint32, storage uint32, init uint32) uint32 {
	id := b.id()
	if init != 0 {
		op(&b.types, opVariable, ptrType, id, storage, init)
	} else {
		op(&b.types, opVariable, ptrType, id, storage)
	}
	b.globals = append(b.globals, id)
	return id
}

// assemble lays the module out: the header, then the sections in
// the order the specification fixes.
func (b *builder) assemble() []uint32 {
	out := []uint32{magic, version, 0, b.bound + 1, 0}
	for _, s := range [][]uint32{b.caps, b.exts, b.imports, b.memory, b.entry, b.modes, b.debug, b.decor, b.types, b.funcs} {
		out = append(out, s...)
	}
	return out
}
