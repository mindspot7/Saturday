// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// GroupWorkgroup is a group entry's workgroup width, the unit the
// Vulkan backend widens a group row in (GroupThreads).
const GroupWorkgroup = 128

// workgroup is the local size an entry is emitted with, chosen from
// the entry's form: 8x8 for a span entry, GroupWorkgroup x 1 for a
// group entry, and 64x1 for an index entry.
func workgroup(en compile.Entry) [2]int {
	switch {
	case en.Span:
		return [2]int{8, 8}
	case en.Group:
		return [2]int{GroupWorkgroup, 1}
	}
	return [2]int{64, 1}
}

// shell emits an entry point's shell: the buffer variables at set 0
// in binding order with the params buffer after the data buffers,
// the push block holding the dispatch record and the word counts,
// the built-in inputs, and the main function, which loads them and
// calls the IR wrapper, which holds the logic. The entry point's
// interface is every variable the builder declared.
func (m *module) shell(en compile.Entry) EntryPoint {
	b := m.b
	k := EntryPoint{Buffers: en.Buffers, ParamsBinding: -1, Workgroup: workgroup(en), Group: en.Group}

	var bufVars []uint32
	for _, bb := range en.Buffers {
		v := b.global(m.bufferPtr(bb.Type), storageStorageBuffer, 0)
		op(&b.decor, opDecorate, v, decorationDescriptorSet, 0)
		op(&b.decor, opDecorate, v, decorationBinding, uint32(bb.Binding))
		bufVars = append(bufVars, v)
		if bb.Type.Dynamic {
			k.BufferLengths = true
		}
	}
	// The wrapper's params buffer parameter follows its data buffers
	// (module.params).
	paramsVar := m.params
	if paramsVar != 0 {
		k.ParamsBinding = len(en.Buffers)
	}

	// The push block: the PushWords, then the word counts
	// by data buffer binding when the entry has a variable-length
	// buffer.
	lengths := 0
	if k.BufferLengths {
		lengths = len(en.Buffers)
	}
	k.PushSize = 4 * (PushWords + lengths)
	members := make([]uint32, PushWords)
	for j := range members {
		members[j] = b.tU32()
	}
	if lengths > 0 {
		arr := b.id()
		op(&b.types, opTypeArray, arr, b.tU32(), b.constU32(uint32(lengths)))
		op(&b.decor, opDecorate, arr, decorationArrayStride, 4)
		b.laidOut[arr] = true
		members = append(members, arr)
	}
	pushType := b.id()
	op(&b.types, opTypeStruct, append([]uint32{pushType}, members...)...)
	op(&b.decor, opDecorate, pushType, decorationBlock)
	for j := range members {
		op(&b.decor, opMemberDecorate, pushType, uint32(j), decorationOffset, uint32(4*j))
	}
	b.laidOut[pushType] = true
	pushVar := b.global(b.ptr(storagePushConstant, pushType), storagePushConstant, 0)

	input := b.ptr(storageInput, b.tUVec3())
	builtin := func(which uint32) uint32 {
		v := b.global(input, storageInput, 0)
		op(&b.decor, opDecorate, v, decorationBuiltIn, which)
		return v
	}
	var gidVar, wgVar, lidVar, nwgVar uint32
	if en.Group {
		wgVar, lidVar, nwgVar = builtin(builtInWorkgroupId), builtin(builtInLocalInvocationId), builtin(builtInNumWorkgroups)
	} else {
		gidVar = builtin(builtInGlobalInvocationId)
	}

	main := b.id()
	f := &fn{m: m, b: b, decl: fnDecl{id: main, ret: b.tVoid(), typ: b.fnType(b.tVoid(), nil)}}
	var head []uint32
	op(&head, opFunction, b.tVoid(), main, 0, f.decl.typ)
	entry := b.id()
	op(&head, opLabel, entry)

	pushWord := func(j uint32) uint32 {
		return f.load(b.tU32(), f.result(opAccessChain, b.ptr(storagePushConstant, b.tU32()), pushVar, b.constU32(j)))
	}
	var args []uint32
	for j, bb := range en.Buffers {
		args = append(args, bufVars[j])
		if bb.Type.Dynamic {
			// The length word the host supplies is the binding's word
			// count; a byte buffer's hidden count is its bytes.
			n := f.load(b.tU32(), f.result(opAccessChain, b.ptr(storagePushConstant, b.tU32()), pushVar, b.constU32(PushWords), b.constU32(uint32(bb.Binding))))
			if !bb.Type.Word {
				n = f.result(opIMul, b.tU32(), n, b.constU32(4))
			}
			args = append(args, n)
		}
	}
	if paramsVar != 0 {
		args = append(args, paramsVar)
	}
	for j := uint32(0); j < 5; j++ {
		args = append(args, pushWord(j))
	}
	component := func(v uint32, c uint32) uint32 {
		return f.result(opCompositeExtract, b.tU32(), f.load(b.tUVec3(), v), c)
	}
	if en.Group {
		// The thread's index across the row, the row, and the row's
		// thread count: the workgroup width by the workgroups across.
		w := b.constU32(uint32(k.Workgroup[0]))
		gx := f.result(opIAdd, b.tU32(), f.result(opIMul, b.tU32(), component(wgVar, 0), w), component(lidVar, 0))
		gy := component(wgVar, 1)
		extra := f.result(opIMul, b.tU32(), w, component(nwgVar, 0))
		args = append(args, gx, gy, extra)
	} else {
		// The thread base of a piece of a split dispatch, zero for
		// an unsplit one.
		gx := f.result(opIAdd, b.tU32(), component(gidVar, 0), pushWord(ThreadBaseWord))
		args = append(args, gx, component(gidVar, 1), b.constU32(0))
	}
	d := m.fns[en.Wrapper]
	f.result(opFunctionCall, d.ret, append([]uint32{d.id}, args...)...)
	f.emit(opReturn)
	b.funcs = append(b.funcs, head...)
	b.funcs = append(b.funcs, f.body...)
	op(&b.funcs, opFunctionEnd)

	// The interface is every module-scope variable: the buffers, the
	// push block, the tables and the row address, and the built-ins.
	op(&b.entry, opEntryPoint, append(append([]uint32{executionModelGLCompute, main}, words("main")...), b.globals...)...)
	op(&b.modes, opExecutionMode, main, executionModeLocalSize, uint32(k.Workgroup[0]), uint32(k.Workgroup[1]), 1)
	op(&b.debug, opName, append([]uint32{main}, words("main")...)...)
	return k
}
