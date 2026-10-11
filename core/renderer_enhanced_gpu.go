// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/backend"
)

// enhancedGPURenderer renders on the host GPU as the GPU renderer
// does, at N times the native resolution on both axes (N = 1 <<
// shift): at each row's setup point it derives the row's N sub-row
// records from the VDP2's record (scaleRow), as the enhanced software
// renderer does, and its GPU renderer core places them at the row's
// slots when it uploads the row's batch and renders them through the
// same entry points at the scaled width.
//
// It draws the VDP1 frame buffer into its device pair at N times each
// axis while the walk keeps the CPU pair, as the enhanced software
// renderer does: the core takes the CPU's frame buffer writes, whose
// copy writes each byte's block, and the list's entries, each scaled
// (scaleOp) and drawn in the core's rounds. CPU reads of the frame
// buffer are served by the CPU pair, so there is no pair sync.
type enhancedGPURenderer struct {
	*gpuRenderer
}

// openEnhancedGPURenderer builds the compute sources on the
// platform's backend and makes the enhanced GPU renderer on it at the
// scale shift.
func openEnhancedGPURenderer(v *VDP2, v1 *VDP1, shift uint32) (*enhancedGPURenderer, error) {
	parent, subs := ComputeSources()
	prog, err := backend.Open(parent, subs...)
	if err != nil {
		return nil, err
	}
	return newEnhancedGPURenderer(v, v1, prog, gpuBatchLines, shift)
}

// newEnhancedGPURenderer makes the enhanced GPU renderer on the
// compute sources built on a device: a GPU renderer core at the scale
// shift submitting every batchRows rows, and the core's store of the
// pending rows' sub-row records. The device pair starts empty, as the
// enhanced software renderer's does (the core clears it at open), and
// every byte of both CPU buffers is marked written (resyncFB), so the
// first take expands the whole CPU pair into it. On failure the device
// is closed.
func newEnhancedGPURenderer(v *VDP2, v1 *VDP1, prog *hostgpu.Program, batchRows int, shift uint32) (*enhancedGPURenderer, error) {
	g, err := newGPURenderer(v, v1, prog, batchRows, shift)
	if err != nil {
		return nil, err
	}
	g.enhanced = true
	g.subRows = make([]lineRecord, g.batchRows<<shift)
	v1.resyncFB()
	return &enhancedGPURenderer{g}, nil
}

// beginLine derives row y's sub-row records into the core's store,
// where the upload of the row's batch takes them. It reads the
// rotation state as the row's setup left it (scaleRow), so it runs
// here and not at the upload.
func (e *enhancedGPURenderer) beginLine(y int) {
	v := e.vdp2
	if !v.lineActive {
		return
	}
	native := v.rowRecord(y)
	i := (y % e.batchRows) << e.shift
	for k := range 1 << e.shift {
		v.scaleRow(&e.subRows[i+k], native, int32(k), e.shift)
	}
}

// syncPair does nothing: the walk keeps the CPU pair.
func (e *enhancedGPURenderer) syncPair(int) {}

func (e *enhancedGPURenderer) backend() string { return backend.Name }

func (e *enhancedGPURenderer) device() string { return e.prog.Device.Name() }
