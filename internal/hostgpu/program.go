// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import (
	"fmt"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// Program is the compute sources built on a device: the device and
// the pipelines by entry point name.
type Program struct {
	Device    Device
	pipelines map[string]Pipeline
}

// Build compiles the compute sources, the parent package first, to the
// compute IR, has the device build the IR into pipelines, and returns
// them by entry point name. An entry point name repeated across the
// sources is an error, since a name must resolve to one pipeline. A
// runtime fault in the compile stage or the device's build is
// returned as an error, so the renderer starts in software instead of
// the process ending.
func Build(dev Device, parent Source, subs ...Source) (out *Program, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("build fault: %v", r)
		}
	}()
	prog, err := compile.Load(parent, subs...)
	if err != nil {
		return nil, err
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		return nil, err
	}
	names := make(map[string]int, len(mod.Entries))
	for i, e := range mod.Entries {
		name := mod.Funcs[e.Func].Name
		if _, dup := names[name]; dup {
			return nil, fmt.Errorf("entry point %s is declared twice", name)
		}
		names[name] = i
	}
	pipes, err := dev.Build(mod)
	if err != nil {
		return nil, err
	}
	if len(pipes) != len(mod.Entries) {
		return nil, fmt.Errorf("%s built %d pipelines for %d entry points", dev.Name(), len(pipes), len(mod.Entries))
	}
	out = &Program{Device: dev, pipelines: make(map[string]Pipeline, len(pipes))}
	for name, i := range names {
		out.pipelines[name] = pipes[i]
	}
	return out, nil
}

// Pipeline returns the pipeline of the named entry point.
func (p *Program) Pipeline(name string) (Pipeline, error) {
	pipe, ok := p.pipelines[name]
	if !ok {
		return nil, fmt.Errorf("no entry point %s", name)
	}
	return pipe, nil
}
