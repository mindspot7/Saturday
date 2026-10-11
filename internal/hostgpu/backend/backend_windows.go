// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && !vulkan

package backend

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/dx12"
)

// Name is the backend Open opens, for display.
const Name = "Direct3D 12"

// open opens the platform's backend, DX12 on the first hardware
// adapter with the renderer's embedded shaders.
func open() (hostgpu.Device, error) {
	return dx12.Open(dx12.RendererShaders())
}
