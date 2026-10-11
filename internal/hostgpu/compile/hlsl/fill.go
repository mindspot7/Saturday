// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hlsl

// The DX12 backend's fill kernel, which runs a fill: a compute list
// has no buffer fill command, and a UAV clear needs descriptor heaps
// the backend does not keep. The kernel is the backend's own, not a
// compute package entry, and the generator compiles it into the
// backend shader set under FillEntry. The device binds the buffer at
// the fill's first byte as the one root UAV and pushes the root
// constants at the positions below: the words to write, the value,
// and the first word of a piece of a split dispatch, since the device
// issues a fill wider than one dispatch in pieces as it does an index
// grid. FillLayout is the root signature the source declares.
const (
	FillEntry     = "Fill"
	FillCountWord = 0
	FillValueWord = 1
	FillBaseWord  = 2
	FillConstants = 3
	FillWorkgroup = 64
)

// FillSource is the fill kernel.
const FillSource = `RWStructuredBuffer<uint> b : register(u0);
cbuffer Fill : register(b0) {
	uint f_count; uint f_value; uint f_base;
};

[numthreads(64, 1, 1)]
[RootSignature("RootFlags(0), UAV(u0), RootConstants(num32BitConstants=3, b0)")]
void main(uint3 dtid : SV_DispatchThreadID) {
	uint i = dtid.x + f_base;
	if (i < f_count) {
		b[i] = f_value;
	}
}
`

// FillLayout is the fill kernel's root signature as the device binds
// it: one root UAV and the FillConstants, at the fill's workgroup.
func FillLayout() Layout {
	return Layout{Buffers: 1, Constants: FillConstants, Workgroup: [2]int{FillWorkgroup, 1}}
}
