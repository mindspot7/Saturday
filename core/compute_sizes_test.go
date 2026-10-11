// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestGPUBufferSizes pins the compute package's buffer sizes to
// the renderer's buffers, so the slice-to-buffer conversions at the
// renderer boundary cannot fail.
func TestGPUBufferSizes(t *testing.T) {
	if compute.VDP2VRAMSize != vdp2VRAMSize || compute.CRAMSize != vdp2CRAMSize {
		t.Error("VDP2 memory sizes differ")
	}
	if compute.VDP1VRAMSize != vdp1VRAMSize || compute.VDP1FBSize != vdp1FBSize {
		t.Error("VDP1 memory sizes differ")
	}
	if vdp2VRAMSize%compute.PageBytes != 0 || vdp2CRAMSize%compute.PageBytes != 0 || compute.PageBytes%4 != 0 {
		t.Errorf("PageBytes %d does not divide VRAM and CRAM into whole pages of whole words", compute.PageBytes)
	}
	if vdp2VRAMPages%64 != 0 || vdp2CRAMPages > 64 {
		t.Errorf("the dirty map words do not cover %d VRAM pages and %d CRAM pages", vdp2VRAMPages, vdp2CRAMPages)
	}
	if gpuStoreVRAMPages < vdp2VRAMPages || gpuStoreCRAMPages < vdp2CRAMPages {
		t.Errorf("a page store (%d, %d slots) cannot hold a whole memory (%d, %d pages)", gpuStoreVRAMPages, gpuStoreCRAMPages, vdp2VRAMPages, vdp2CRAMPages)
	}
	if compute.PageTableWords < gpuStoreVRAMPages || compute.PageTableWords < gpuStoreCRAMPages {
		t.Errorf("PageTableWords %d below the store slots (%d, %d)", compute.PageTableWords, gpuStoreVRAMPages, gpuStoreCRAMPages)
	}
}
