// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package backend

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/metal"
)

// Name is the backend Open opens, for display.
const Name = "Metal"

// open opens the platform's backend, Metal.
func open() (hostgpu.Device, error) {
	return metal.Open()
}
