// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dx12

import (
	"fmt"
	"reflect"
	"testing"
	"unsafe"

	"github.com/user-none/erings/internal/hostgpu"
)

// TestStructSizes pins the mirrored structures to the sizes the
// headers give them.
func TestStructSizes(t *testing.T) {
	for _, s := range []struct {
		name string
		size uintptr
		want uintptr
	}{
		{"commandQueueDesc", unsafe.Sizeof(commandQueueDesc{}), 16},
		{"heapProperties", unsafe.Sizeof(heapProperties{}), 20},
		{"resourceDesc", unsafe.Sizeof(resourceDesc{}), 56},
		{"computePipelineStateDesc", unsafe.Sizeof(computePipelineStateDesc{}), 56},
		{"resourceBarrier", unsafe.Sizeof(resourceBarrier{}), 32},
		{"featureDataShaderModel", unsafe.Sizeof(featureDataShaderModel{}), 4},
		{"featureDataArchitecture", unsafe.Sizeof(featureDataArchitecture{}), 16},
		{"featureDataOptions1", unsafe.Sizeof(featureDataOptions1{}), 24},
		{"adapterDesc1", unsafe.Sizeof(adapterDesc1{}), 312},
		{"guid", unsafe.Sizeof(guid{}), 16},
	} {
		if s.size != s.want {
			t.Errorf("%s is %d bytes, want %d", s.name, s.size, s.want)
		}
	}
}

// TestCustomHeap holds the custom heaps to the properties the standard
// heap types have on each architecture: an upload heap write-combined
// except on a cache-coherent UMA adapter, a readback heap write-back
// everywhere, both in the L0 pool.
func TestCustomHeap(t *testing.T) {
	discrete := featureDataArchitecture{}
	uma := featureDataArchitecture{uma: 1}
	coherent := featureDataArchitecture{uma: 1, cacheCoherentUMA: 1}
	for _, c := range []struct {
		name string
		typ  uint32
		arch featureDataArchitecture
		page uint32
	}{
		{"upload discrete", heapTypeUpload, discrete, cpuPageWriteCombine},
		{"upload uma", heapTypeUpload, uma, cpuPageWriteCombine},
		{"upload cache-coherent uma", heapTypeUpload, coherent, cpuPageWriteBack},
		{"readback discrete", heapTypeReadback, discrete, cpuPageWriteBack},
		{"readback uma", heapTypeReadback, uma, cpuPageWriteBack},
		{"readback cache-coherent uma", heapTypeReadback, coherent, cpuPageWriteBack},
	} {
		want := heapProperties{typ: heapTypeCustom, cpuPageProperty: c.page, memoryPool: memoryPoolL0, creationNodeMask: 1, visibleNodeMask: 1}
		if got := customHeap(c.typ, c.arch); got != want {
			t.Errorf("%s: %+v, want %+v", c.name, got, want)
		}
	}
}

// TestChoosePlacement holds each kind's heaps, in order, to the
// custom heap properties, and an upload kind on a cache-coherent UMA
// adapter, where the two heaps are the same, to one heap.
func TestChoosePlacement(t *testing.T) {
	upload := heapProperties{typ: heapTypeCustom, cpuPageProperty: cpuPageWriteCombine, memoryPool: memoryPoolL0, creationNodeMask: 1, visibleNodeMask: 1}
	readback := heapProperties{typ: heapTypeCustom, cpuPageProperty: cpuPageWriteBack, memoryPool: memoryPoolL0, creationNodeMask: 1, visibleNodeMask: 1}
	working := heapProperties{typ: heapTypeDefault, creationNodeMask: 1, visibleNodeMask: 1}
	want := placement{
		hostgpu.BufferUpload:   {readback, upload},
		hostgpu.BufferReadback: {readback},
		hostgpu.BufferWorking:  {working},
		hostgpu.BufferOutput:   {readback},
	}
	if got := choosePlacement(upload, readback); !reflect.DeepEqual(got, want) {
		t.Errorf("placement %+v, want %+v", got, want)
	}
	want[hostgpu.BufferUpload] = []heapProperties{readback}
	if got := choosePlacement(readback, readback); !reflect.DeepEqual(got, want) {
		t.Errorf("cache-coherent UMA placement %+v, want %+v", got, want)
	}
}

// guidText is an identifier in its canonical text form: the first
// three fields in host order, the last eight bytes in text order.
func guidText(g guid) string {
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x", g.data1, g.data2, g.data3,
		g.data4[0], g.data4[1], g.data4[2], g.data4[3], g.data4[4], g.data4[5], g.data4[6], g.data4[7])
}

// TestInterfaceIDs pins each interface identifier literal to its
// canonical text, so a mistyped field or byte shows.
func TestInterfaceIDs(t *testing.T) {
	ids := []struct {
		name string
		id   guid
		text string
	}{
		{"device", iidDevice, "189819f1-1db6-4b57-be54-1821339b85f7"},
		{"command queue", iidCommandQueue, "0ec870a6-5d7e-4c22-8cfc-5baae07616ed"},
		{"allocator", iidAllocator, "6102dee4-af59-4b09-b999-b44d73f09b24"},
		{"command list", iidCommandList, "5b160d0f-ac1b-4185-8ba8-b3ae42a5a455"},
		{"resource", iidResource, "696442be-a72e-4059-bc79-5b5c98040fad"},
		{"fence", iidFence, "0a753dcf-c4d8-4b91-adf6-be5a60d95a76"},
		{"pipeline state", iidPipelineState, "765a30f3-f624-4c6f-a828-ace948622445"},
		{"root signature", iidRootSignature, "c54a6b66-72df-4ee8-8be5-a946a1429214"},
		{"factory 4", iidFactory4, "1bc6ea02-ef36-464f-bf0c-21ca39e5168a"},
		{"adapter 1", iidAdapter1, "29038f61-3839-4626-91fd-086879011a05"},
	}
	for _, id := range ids {
		if got := guidText(id.id); got != id.text {
			t.Errorf("%s: %s, want %s", id.name, got, id.text)
		}
	}
}
