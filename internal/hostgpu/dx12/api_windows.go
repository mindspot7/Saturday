// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dx12

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/user-none/erings/internal/hostgpu"
)

// Direct3D 12 and DXGI constants used here, from the public headers.
const (
	featureLevel11_0        = 0xb000
	featureArchitecture     = 1
	featureShaderModel      = 7
	featureOptions1         = 8
	shaderModel6_0          = 0x60
	commandListTypeCompute  = 2
	heapTypeDefault         = 1
	heapTypeUpload          = 2
	heapTypeReadback        = 3
	heapTypeCustom          = 4
	cpuPageWriteCombine     = 2
	cpuPageWriteBack        = 3
	memoryPoolL0            = 1
	resourceDimensionBuffer = 1
	textureLayoutRowMajor   = 1
	resourceFlagUAV         = 0x4
	resourceStateCommon     = 0
	resourceStateUAV        = 0x8
	resourceStateCopyDest   = 0x400
	resourceStateCopySource = 0x800
	barrierTypeTransition   = 0
	barrierTypeUAV          = 2
	barrierAllSubresources  = 0xffffffff
	adapterFlagSoftware     = 2
	dispatchMaxGroups       = 65535 // D3D12_CS_DISPATCH_MAX_THREAD_GROUPS_PER_DIMENSION
)

// Vtable slots of the interfaces called, counting the three IUnknown
// methods first. The header lists a struct-returning method twice
// for two ABIs, and the binary vtable holds one slot for it.
const (
	unknownRelease = 2

	deviceCreateCommandQueue         = 8
	deviceCreateCommandAllocator     = 9
	deviceCreateComputePipelineState = 11
	deviceCreateCommandList          = 12
	deviceCheckFeatureSupport        = 13
	deviceCreateRootSignature        = 16
	deviceCreateCommittedResource    = 27
	deviceCreateFence                = 36
	deviceGetDeviceRemovedReason     = 37

	queueExecuteCommandLists = 10
	queueSignal              = 14

	listClose                             = 9
	listReset                             = 10
	listDispatch                          = 14
	listCopyBufferRegion                  = 15
	listSetPipelineState                  = 25
	listResourceBarrier                   = 26
	listSetComputeRootSignature           = 29
	listSetComputeRoot32BitConstants      = 35
	listSetComputeRootUnorderedAccessView = 41

	allocatorReset = 8

	resourceMap                  = 8
	resourceUnmap                = 9
	resourceGetGPUVirtualAddress = 11

	fenceGetCompletedValue    = 8
	fenceSetEventOnCompletion = 9
	fenceSignal               = 10

	factoryEnumAdapters1 = 12
	adapterGetDesc1      = 10
)

// dxgiErrorNotFound is the result EnumAdapters1 returns past the last
// adapter.
const dxgiErrorNotFound = 0x887A0002

// guid is a COM interface identifier in its binary layout.
type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

// The interface identifiers, each in its binary layout with the
// canonical text, eight-four-four-four-twelve hex digits, beside it:
// the first three fields in host order, the last eight bytes in text
// order.
var (
	iidDevice        = guid{0x189819f1, 0x1db6, 0x4b57, [8]byte{0xbe, 0x54, 0x18, 0x21, 0x33, 0x9b, 0x85, 0xf7}} // 189819f1-1db6-4b57-be54-1821339b85f7
	iidCommandQueue  = guid{0x0ec870a6, 0x5d7e, 0x4c22, [8]byte{0x8c, 0xfc, 0x5b, 0xaa, 0xe0, 0x76, 0x16, 0xed}} // 0ec870a6-5d7e-4c22-8cfc-5baae07616ed
	iidAllocator     = guid{0x6102dee4, 0xaf59, 0x4b09, [8]byte{0xb9, 0x99, 0xb4, 0x4d, 0x73, 0xf0, 0x9b, 0x24}} // 6102dee4-af59-4b09-b999-b44d73f09b24
	iidCommandList   = guid{0x5b160d0f, 0xac1b, 0x4185, [8]byte{0x8b, 0xa8, 0xb3, 0xae, 0x42, 0xa5, 0xa4, 0x55}} // 5b160d0f-ac1b-4185-8ba8-b3ae42a5a455
	iidResource      = guid{0x696442be, 0xa72e, 0x4059, [8]byte{0xbc, 0x79, 0x5b, 0x5c, 0x98, 0x04, 0x0f, 0xad}} // 696442be-a72e-4059-bc79-5b5c98040fad
	iidFence         = guid{0x0a753dcf, 0xc4d8, 0x4b91, [8]byte{0xad, 0xf6, 0xbe, 0x5a, 0x60, 0xd9, 0x5a, 0x76}} // 0a753dcf-c4d8-4b91-adf6-be5a60d95a76
	iidPipelineState = guid{0x765a30f3, 0xf624, 0x4c6f, [8]byte{0xa8, 0x28, 0xac, 0xe9, 0x48, 0x62, 0x24, 0x45}} // 765a30f3-f624-4c6f-a828-ace948622445
	iidRootSignature = guid{0xc54a6b66, 0x72df, 0x4ee8, [8]byte{0x8b, 0xe5, 0xa9, 0x46, 0xa1, 0x42, 0x92, 0x14}} // c54a6b66-72df-4ee8-8be5-a946a1429214
	iidFactory4      = guid{0x1bc6ea02, 0xef36, 0x464f, [8]byte{0xbf, 0x0c, 0x21, 0xca, 0x39, 0xe5, 0x16, 0x8a}} // 1bc6ea02-ef36-464f-bf0c-21ca39e5168a
	iidAdapter1      = guid{0x29038f61, 0x3839, 0x4626, [8]byte{0x91, 0xfd, 0x08, 0x68, 0x79, 0x01, 0x1a, 0x05}} // 29038f61-3839-4626-91fd-086879011a05
)

// The structures passed by pointer, field for field from the headers.
// Go's natural alignment reproduces the C layout on amd64 and arm64.

type commandQueueDesc struct {
	typ      uint32
	priority int32
	flags    uint32
	nodeMask uint32
}

type heapProperties struct {
	typ, cpuPageProperty, memoryPool, creationNodeMask, visibleNodeMask uint32
}

type resourceDesc struct {
	dimension        uint32
	_                uint32
	alignment        uint64
	width            uint64
	height           uint32
	depthOrArraySize uint16
	mipLevels        uint16
	format           uint32
	sampleCount      uint32
	sampleQuality    uint32
	layout           uint32
	flags            uint32
}

type computePipelineStateDesc struct {
	rootSignature uintptr
	code          uintptr
	codeLength    uintptr
	nodeMask      uint32
	_             uint32
	cachedBlob    uintptr
	cachedLength  uintptr
	flags         uint32
	_             uint32
}

// resourceBarrier is D3D12_RESOURCE_BARRIER in the transition form,
// whose union is the largest: the type, the flags, the resource, the
// subresource, and the states before and after. The UAV form uses the
// resource alone, nil for every UAV, and leaves the rest zero.
type resourceBarrier struct {
	typ         uint32
	flags       uint32
	resource    uintptr
	subresource uint32
	before      uint32
	after       uint32
	_           uint32
}

type featureDataShaderModel struct{ highest uint32 }

type featureDataArchitecture struct {
	nodeIndex                                uint32
	tileBasedRenderer, uma, cacheCoherentUMA int32
}

type featureDataOptions1 struct {
	waveOps, laneCountMin, laneCountMax, totalLaneCount, expandedComputeStates, int64ShaderOps uint32
}

type adapterDesc1 struct {
	description                                   [128]uint16
	vendorID, deviceID, subSysID, revision        uint32
	dedicatedVideo, dedicatedSystem, sharedSystem uintptr
	luidLow                                       uint32
	luidHigh                                      int32
	flags                                         uint32
}

var (
	loadOnce sync.Once
	loadErr  error

	createDevice   uintptr // D3D12CreateDevice
	createFactory2 uintptr // CreateDXGIFactory2
)

// load resolves the Direct3D 12 and DXGI entry points from the system
// directory, once. A missing library or symbol is ErrUnavailable.
func load() error {
	loadOnce.Do(func() {
		type sym struct {
			dst  *uintptr
			name string
		}
		resolve := func(lib string, syms ...sym) {
			if loadErr != nil {
				return
			}
			h, err := windows.LoadLibraryEx(lib, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
			if err != nil {
				loadErr = fmt.Errorf("%w: %s: %v", hostgpu.ErrUnavailable, lib, err)
				return
			}
			for _, s := range syms {
				if *s.dst, err = windows.GetProcAddress(h, s.name); err != nil {
					loadErr = fmt.Errorf("%w: %s: %v", hostgpu.ErrUnavailable, s.name, err)
					return
				}
			}
		}
		resolve("d3d12.dll", sym{&createDevice, "D3D12CreateDevice"})
		resolve("dxgi.dll", sym{&createFactory2, "CreateDXGIFactory2"})
	})
	return loadErr
}

// argSlots is the argument array's size: the object and up to nine
// arguments, the most any call here passes.
const argSlots = 10

// object is the start of every COM object: the pointer to its method
// table. The objects are the runtime's memory, which Go neither moves
// nor frees, so a handle is read through this layout by value.
type object struct {
	vtable *[64]uintptr
}

// method is the address of the method in the object's vtable slot.
func method(obj uintptr, slot int) uintptr {
	o := *(**object)(unsafe.Pointer(&obj))
	return o.vtable[slot]
}

// com calls the method in the object's vtable slot with the object as
// the first argument, on the argument array, which is retained memory
// so the call allocates nothing. The array holds the object and the
// most arguments any call here passes (argSlots).
func com(slots *[argSlots]uintptr, obj uintptr, slot int, args ...uintptr) uintptr {
	fn := method(obj, slot)
	slots[0] = obj
	n := copy(slots[1:], args)
	r, _, _ := syscall.SyscallN(fn, slots[:1+n]...)
	return r
}

// call calls a free function on the argument array, as com does a
// method.
func call(slots *[argSlots]uintptr, fn uintptr, args ...uintptr) uintptr {
	n := copy(slots[:], args)
	r, _, _ := syscall.SyscallN(fn, slots[:n]...)
	return r
}

// hresult turns a COM return into an error, the code in hex.
func hresult(r uintptr, what string) error {
	if int32(r) < 0 {
		return fmt.Errorf("%s: 0x%08x", what, uint32(r))
	}
	return nil
}

// release drops one reference on a COM object, if any.
func release(slots *[argSlots]uintptr, obj *uintptr) {
	if *obj != 0 {
		com(slots, *obj, unknownRelease)
		*obj = 0
	}
}
