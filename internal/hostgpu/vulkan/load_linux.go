// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package vulkan

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"

	"github.com/user-none/erings/internal/hostgpu"
)

var (
	loadOnce            sync.Once
	loadErr             error
	getInstanceProcAddr uintptr
)

// load opens the system loader and resolves vkGetInstanceProcAddr,
// once. A missing loader is ErrUnavailable.
func load() error {
	loadOnce.Do(func() {
		lib, err := purego.Dlopen("libvulkan.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			loadErr = fmt.Errorf("%w: %v", hostgpu.ErrUnavailable, err)
			return
		}
		if getInstanceProcAddr, err = purego.Dlsym(lib, "vkGetInstanceProcAddr"); err != nil {
			loadErr = fmt.Errorf("%w: %v", hostgpu.ErrUnavailable, err)
		}
	})
	return loadErr
}
