// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package conformance

import (
	"fmt"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	rules "github.com/user-none/erings/core/compute/conformance"
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// RoundStateEntries names the conformance entries for the round
// state's shared-word helpers: group entries over the RoundState
// buffer group, one region and one operation each, with a params row
// per group.
var RoundStateEntries = [...]string{"RaiseWords", "AddWords", "SwapChain"}

// RoundStateSources builds the round state conformance entries.
func RoundStateSources() (parent, sub compile.Source) {
	parent, sub = Sources()
	sub.Entries = append(sub.Entries, RoundStateEntries[:]...)
	return
}

// RoundStateGroups is the groups each round state entry is dispatched
// over, each with its own params row, so the two rows of Probes
// serve.
const RoundStateGroups = 2

// RoundStateThreads is the threads a test gives each group, more than
// one workgroup on every backend so the group form spanning
// workgroups is what the device runs. A backend rounds it up to whole
// workgroups of its own size, and the entries record the group size
// they ran with.
const RoundStateThreads = 512

// roundStateMaxThreads is the most threads a group's recorded size
// may name: four words each fill the group's 65536-word range.
const roundStateMaxThreads = 65536 / 4

// RoundStateWords is the length of each region a round state test
// binds: a power of two, as every variable-length buffer's is, past
// every word the entries touch.
const RoundStateWords = 1 << 18

// NewRoundState allocates a round state with every region at
// RoundStateWords, zeroed.
func NewRoundState() *compute.RoundState {
	return &compute.RoundState{
		Stamps: make(compute.OrderStamps, RoundStateWords),
		Heads:  make(compute.FragmentHeads, RoundStateWords),
		Pool:   make(compute.FragmentPool, RoundStateWords),
	}
}

// RoundStateOf views three buffers' bytes as a round state,
// in the group's field order.
func RoundStateOf(stamps, heads, pool []byte) *compute.RoundState {
	words := func(b []byte) []uint32 {
		return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
	}
	return &compute.RoundState{Stamps: words(stamps), Heads: words(heads), Pool: words(pool)}
}

// roundStateRules is each round state entry's Go function, by name.
var roundStateRules = map[string]func(rs *compute.RoundState, p *rules.Probe, t, n, g uint32){
	"RaiseWords": rules.RaiseWords,
	"AddWords":   rules.AddWords,
	"SwapChain":  rules.SwapChain,
}

// RunRoundStateGo runs one round state entry on the Go side: one
// thread per group, which runs every thread's work in order. An
// unknown name is an error.
func RunRoundStateGo(name string, rs *compute.RoundState) error {
	return RunRoundStateThreads(name, rs, 1)
}

// RunRoundStateThreads runs one round state entry on the Go side with
// n threads per group, each group's threads in order. An unknown name
// is an error.
func RunRoundStateThreads(name string, rs *compute.RoundState, n uint32) error {
	rule, ok := roundStateRules[name]
	if !ok {
		return fmt.Errorf("unknown round state entry %s", name)
	}
	probes := Probes()
	for g := uint32(0); g < RoundStateGroups; g++ {
		for t := uint32(0); t < n; t++ {
			rule(rs, &probes[g], t, n, g)
		}
	}
	return nil
}

// CheckRoundState holds a backend's result for one round state
// entry, run over RoundStateGroups groups of threads threads each.
// The raise entry must equal the Go execution word for word across
// every region, which also holds the group's binding order: a
// backend binding the regions out of order raises the wrong one. The
// add entry's ranges land in the device's order and its counter
// counts the threads, so it is held to what any order gives: the
// counter at four per thread and every word of the reserved ranges
// marked with the group's value. The swap entry's chains are walked:
// from each group's head, every thread's slot once. The add and swap
// entries are held to the group size each group recorded, which must
// be at least asked, the threads the test gave each group, and every
// word outside the counters, the sizes, and the ranges they touch,
// in their own region and the other two, must equal the Go
// execution's, so a stray write shows.
func CheckRoundState(name string, got, want *compute.RoundState, asked uint32) error {
	probes := Probes()
	size := func(words []uint32, g uint32) (uint32, error) {
		n := words[g*8+1]
		if n < asked || n > roundStateMaxThreads {
			return 0, fmt.Errorf("%s: group %d: recorded size %d outside %d to %d", name, g, n, asked, roundStateMaxThreads)
		}
		return n, nil
	}
	// equal holds a region to the Go execution at every word but the
	// ones touched reports.
	equal := func(region string, got, want []uint32, touched func(i uint32) bool) error {
		for i := range want {
			if got[i] != want[i] && (touched == nil || !touched(uint32(i))) {
				return fmt.Errorf("%s: %s word %d: gpu %#x go %#x", name, region, i, got[i], want[i])
			}
		}
		return nil
	}
	// headerOrRange reports the words an entry touches in its region
	// for each group's recorded size: the counter or head, the size,
	// and the range of per words for each thread from the group's base.
	headerOrRange := func(sizes []uint32, per uint32) func(i uint32) bool {
		return func(i uint32) bool {
			for g := uint32(0); g < RoundStateGroups; g++ {
				if i == g*8 || i == g*8+1 {
					return true
				}
				if base := 4096 + g*65536; i >= base && i < base+per*sizes[g*8+1] {
					return true
				}
			}
			return false
		}
	}
	switch name {
	case "RaiseWords":
		for _, r := range []struct {
			name      string
			got, want []uint32
		}{{"stamps", got.Stamps, want.Stamps}, {"heads", got.Heads, want.Heads}, {"pool", got.Pool, want.Pool}} {
			if err := equal(r.name, r.got, r.want, nil); err != nil {
				return err
			}
		}
		return nil
	case "AddWords":
		pool := got.Pool
		for g := uint32(0); g < RoundStateGroups; g++ {
			threads, err := size(pool, g)
			if err != nil {
				return err
			}
			if pool[g*8] != 4*threads {
				return fmt.Errorf("%s: group %d: counter %d, want %d", name, g, pool[g*8], 4*threads)
			}
			for i := uint32(0); i < 4*threads; i++ {
				if pool[4096+g*65536+i] != probes[g].A {
					return fmt.Errorf("%s: group %d: word %d unmarked", name, g, i)
				}
			}
		}
		if err := equal("pool", pool, want.Pool, headerOrRange(pool, 4)); err != nil {
			return err
		}
		if err := equal("stamps", got.Stamps, want.Stamps, nil); err != nil {
			return err
		}
		return equal("heads", got.Heads, want.Heads, nil)
	case "SwapChain":
	default:
		return fmt.Errorf("unknown round state entry %s", name)
	}
	heads := got.Heads
	for g := uint32(0); g < RoundStateGroups; g++ {
		threads, err := size(heads, g)
		if err != nil {
			return err
		}
		seen := make([]bool, threads)
		head := heads[g*8]
		for steps := uint32(0); head != 0; steps++ {
			t := head - 1
			if t >= threads || seen[t] || steps >= threads {
				return fmt.Errorf("%s: group %d: the chain repeats or leaves the group at step %d", name, g, steps)
			}
			seen[t] = true
			head = heads[4096+g*65536+t]
		}
		for t, ok := range seen {
			if !ok {
				return fmt.Errorf("%s: group %d: thread %d is not on the chain", name, g, t)
			}
		}
	}
	if err := equal("heads", heads, want.Heads, headerOrRange(heads, 1)); err != nil {
		return err
	}
	if err := equal("stamps", got.Stamps, want.Stamps, nil); err != nil {
		return err
	}
	return equal("pool", got.Pool, want.Pool, nil)
}
