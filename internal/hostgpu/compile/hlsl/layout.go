// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hlsl

import (
	"fmt"
	"strings"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// Layout is an entry's root signature as the shell declares it and the
// device binds it: one root UAV descriptor per buffer, the data
// buffers in binding order then the params buffer when the entry takes
// one, and after them the 32-bit root constants, the FixedWords
// followed by the module's length words. Every entry of a
// module declares the same constant count, so the device pushes one
// block for every dispatch. The workgroup is the shell's numthreads,
// which the device rounds the grid to, so the blob and the device
// take it from this one rule.
type Layout struct {
	Buffers   int
	Constants int
	Lengths   bool   // the entry binds a variable-length buffer, so the host fills the length words
	Dynamic   uint32 // the variable-length bindings, by bit
	Group     bool   // a group entry, so the host fills RowThreadsWord and never splits the grid
	Workgroup [2]int
}

// GroupWorkgroup is a group entry's numthreads width, the unit the
// DX12 backend widens a group row in (GroupThreads).
const GroupWorkgroup = 128

// workgroup is the numthreads an entry is emitted with, chosen from
// the entry's form: 8x8 for a span entry, GroupWorkgroup x 1 for a
// group entry, and 64x1 for an index entry.
func workgroup(en compile.Entry) [2]int {
	switch {
	case en.Span:
		return [2]int{8, 8}
	case en.Group:
		return [2]int{GroupWorkgroup, 1}
	}
	return [2]int{64, 1}
}

// The root constants of every entry, a contract between the shell and
// the dx12 device alone: the dispatch record's five words, then two
// words the device fills for itself, then the module's length words.
// RowThreadsWord is a group entry's row thread count, which no
// built-in supplies (d_rowthreads). ThreadBaseWord is the first thread
// of a piece of a split index or span dispatch, zero for an unsplit
// one (d_base). FixedWords is the count of the words ahead of the
// lengths.
const (
	RowThreadsWord = 5
	ThreadBaseWord = 6
	FixedWords     = 7
)

// RootDwords is the root signature's size limit in 32-bit words: a
// root descriptor costs two and a root constant one. No entry
// declares more constants than this, so a device's constant block
// of this size holds every entry's.
const RootDwords = 64

// Lengths is the module's length words: the most data buffers any
// entry with a variable-length buffer binds, or zero when none has one.
func Lengths(m *compile.Module) int {
	n := 0
	for _, en := range m.Entries {
		for _, bb := range en.Buffers {
			if bb.Type.Dynamic && len(en.Buffers) > n {
				n = len(en.Buffers)
			}
		}
	}
	return n
}

// EntryLayout is entry i's layout.
func EntryLayout(m *compile.Module, i int) Layout {
	en := m.Entries[i]
	l := Layout{Buffers: len(en.Buffers), Constants: FixedWords + Lengths(m), Group: en.Group, Dynamic: compile.DynamicBindings(en.Buffers), Workgroup: workgroup(en)}
	if len(en.Params) > 0 {
		l.Buffers++
	}
	l.Lengths = l.Dynamic != 0
	return l
}

// check reports a layout past the root signature's size limit.
func (l Layout) check() error {
	if n := 2*l.Buffers + l.Constants; n > RootDwords {
		return fmt.Errorf("root signature of %d dwords exceeds %d", n, RootDwords)
	}
	return nil
}

// RootSignature prints the layout in the root signature language: the
// descriptors at u0 upward, then the constants at b0.
func (l Layout) RootSignature() string {
	var b strings.Builder
	b.WriteString("RootFlags(0)")
	for i := 0; i < l.Buffers; i++ {
		fmt.Fprintf(&b, ", UAV(u%d)", i)
	}
	fmt.Fprintf(&b, ", RootConstants(num32BitConstants=%d, b0)", l.Constants)
	return b.String()
}
