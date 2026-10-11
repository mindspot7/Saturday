// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package backend

import "github.com/user-none/erings/internal/hostgpu"

// Name is empty: the platform has no backend.
const Name = ""

// open reports ErrUnavailable: the platform has no backend.
func open() (hostgpu.Device, error) {
	return nil, hostgpu.ErrUnavailable
}
