// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package backend opens the platform's backend and builds the compute
// sources on it.
package backend

import "github.com/user-none/erings/internal/hostgpu"

// Open opens the platform's backend (open) and builds the compute
// sources on it. A build failure closes the device.
func Open(parent hostgpu.Source, subs ...hostgpu.Source) (*hostgpu.Program, error) {
	dev, err := open()
	if err != nil {
		return nil, err
	}
	prog, err := hostgpu.Build(dev, parent, subs...)
	if err != nil {
		dev.Close()
		return nil, err
	}
	return prog, nil
}
