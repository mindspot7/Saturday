// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || (windows && vulkan)

package backend

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/vulkan"
)

// Name is the backend Open opens, for display.
const Name = "Vulkan"

// open opens the platform's backend, Vulkan.
func open() (hostgpu.Device, error) {
	return vulkan.Open()
}
