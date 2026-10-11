// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package conformance

import (
	"bytes"
	"strings"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// TestSuiteLowers is the compile stage's regression suite on every
// platform: the conformance package passes validation and lowers to
// one entry per conformance function with the form its dispatch
// expects.
func TestSuiteLowers(t *testing.T) {
	mod, err := Lower()
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Entries) != len(Entries) {
		t.Fatalf("entries = %d, want %d", len(mod.Entries), len(Entries))
	}
	for i, e := range mod.Entries {
		if len(e.Buffers) != 3 || len(e.Params) != 1 || e.Span != (Entries[i].Span != nil) || e.Group != (Entries[i].Group != nil) {
			t.Errorf("entry %s: %+v", Entries[i].Name, e)
		}
	}
}

// TestSuiteRunsInGo executes every entry on the Go side over the test
// vectors, which is the reference the backends are compared with.
func TestSuiteRunsInGo(t *testing.T) {
	in := make(compute.Layer, LayerWords)
	mem := new(compute.VDP2VRAM)
	Inputs(in, mem)
	probes := Probes()
	for _, e := range Entries {
		out := make(compute.Layer, LayerWords)
		m := *mem
		RunGo(e, in, &m, out, &probes)
		nonzero := 0
		for i := 0; i < Threads*8; i++ {
			if out[i] != 0 {
				nonzero++
			}
		}
		if nonzero == 0 {
			t.Errorf("%s wrote nothing", e.Name)
		}
	}
}

// TestRoundState lowers the round state entries as group entries
// over the group's three variable-length buffers and checks the Go
// references: the raise ends every word at the peak of the offers
// with the group's own params, the add marks a contiguous range per
// thread and leaves the counter at the total, and the swap chain
// links every thread once. The add and swap checks of CheckRoundState
// pass on the Go result held to itself. TestCheckRoundState holds the
// check to a pairing that differs.
func TestRoundState(t *testing.T) {
	parent, sub := RoundStateSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range RoundStateEntries {
		e := entryNamed(t, mod, name)
		if !e.Group || len(e.Buffers) != 3 || len(e.Params) != 1 {
			t.Fatalf("round state entry lowered as %+v", e)
		}
		for _, bb := range e.Buffers {
			if !bb.Type.Dynamic {
				t.Fatalf("round state region %s lowered as a fixed buffer", bb.Type.Name)
			}
		}
	}
	probes := Probes()
	rs := NewRoundState()
	if err := RunRoundStateGo("RaiseWords", rs); err != nil {
		t.Fatal(err)
	}
	for g := 0; g < RoundStateGroups; g++ {
		for k := uint32(0); k < 8; k++ {
			if got, want := rs.Stamps[uint32(g)*8+k], probes[g].A+2048-min(k, 8-k); got != want {
				t.Errorf("raise: group %d word %d: %#x, want %#x", g, k, got, want)
			}
		}
	}
	for _, name := range RoundStateEntries[1:] {
		rs = NewRoundState()
		if err := RunRoundStateGo(name, rs); err != nil {
			t.Fatal(err)
		}
		if err := CheckRoundState(name, rs, rs, 1); err != nil {
			t.Error(err)
		}
	}
}

// entryNamed is the lowered module's entry for the named function.
func entryNamed(t *testing.T, mod *compile.Module, name string) compile.Entry {
	t.Helper()
	for _, e := range mod.Entries {
		if mod.Funcs[e.Func].Name == name {
			return e
		}
	}
	t.Fatalf("no entry %s", name)
	return compile.Entry{}
}

// runRoundStateThreads runs one round state entry as a device group
// of n threads would, on a fresh round state.
func runRoundStateThreads(t *testing.T, name string, n uint32) *compute.RoundState {
	t.Helper()
	rs := NewRoundState()
	if err := RunRoundStateThreads(name, rs, n); err != nil {
		t.Fatal(err)
	}
	return rs
}

// TestCheckRoundState holds the round state check to the pairing a
// device test gives it, a group of several threads against the Go
// execution as one: a correct result passes, and each corruption the
// check exists to catch fails with its own message.
func TestCheckRoundState(t *testing.T) {
	const n = 3
	const base = 4096
	cases := []struct {
		name, entry, want string
		corrupt           func(rs *compute.RoundState)
	}{
		{"raise stamps", "RaiseWords", "stamps word", func(rs *compute.RoundState) { rs.Stamps[3] ^= 1 }},
		{"raise heads", "RaiseWords", "heads word", func(rs *compute.RoundState) { rs.Heads[3] ^= 1 }},
		{"raise pool", "RaiseWords", "pool word", func(rs *compute.RoundState) { rs.Pool[3] ^= 1 }},
		{"add counter", "AddWords", "counter", func(rs *compute.RoundState) { rs.Pool[0]-- }},
		{"add unmarked", "AddWords", "unmarked", func(rs *compute.RoundState) { rs.Pool[base+5] ^= 1 }},
		{"add stray pool", "AddWords", "pool word", func(rs *compute.RoundState) { rs.Pool[base+4*n+5] = 1 }},
		{"add stray stamps", "AddWords", "stamps word", func(rs *compute.RoundState) { rs.Stamps[7] = 1 }},
		{"add stray heads", "AddWords", "heads word", func(rs *compute.RoundState) { rs.Heads[7] = 1 }},
		{"swap repeats", "SwapChain", "repeats or leaves", func(rs *compute.RoundState) { rs.Heads[base+rs.Heads[0]-1] = rs.Heads[0] }},
		{"swap leaves", "SwapChain", "repeats or leaves", func(rs *compute.RoundState) { rs.Heads[base+rs.Heads[0]-1] = n + 2 }},
		{"swap missing", "SwapChain", "not on the chain", func(rs *compute.RoundState) { rs.Heads[0] = rs.Heads[base+rs.Heads[0]-1] }},
		{"swap stray heads", "SwapChain", "heads word", func(rs *compute.RoundState) { rs.Heads[base+n+5] = 1 }},
		{"swap stray stamps", "SwapChain", "stamps word", func(rs *compute.RoundState) { rs.Stamps[7] = 1 }},
		{"swap stray pool", "SwapChain", "pool word", func(rs *compute.RoundState) { rs.Pool[7] = 1 }},
	}
	want := map[string]*compute.RoundState{}
	for _, name := range RoundStateEntries {
		want[name] = NewRoundState()
		if err := RunRoundStateGo(name, want[name]); err != nil {
			t.Fatal(err)
		}
		if err := CheckRoundState(name, runRoundStateThreads(t, name, n), want[name], n); err != nil {
			t.Errorf("%s: a correct result of %d threads failed: %v", name, n, err)
		}
	}
	for _, c := range cases {
		got := runRoundStateThreads(t, c.entry, n)
		c.corrupt(got)
		err := CheckRoundState(c.entry, got, want[c.entry], n)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.want)
		}
	}
}

// TestRoundStateRecordedSize holds the add and swap checks to the
// group size the entries record rather than the threads the test
// asked for: a group run over more threads than asked, as a backend's
// rounding to whole workgroups gives, passes, and a recorded size
// below the asked threads or past the group's range fails.
func TestRoundStateRecordedSize(t *testing.T) {
	for _, name := range RoundStateEntries[1:] {
		rs := runRoundStateThreads(t, name, 3)
		if err := CheckRoundState(name, rs, rs, 2); err != nil {
			t.Errorf("%s: three threads for two asked: %v", name, err)
		}
		if err := CheckRoundState(name, rs, rs, 4); err == nil {
			t.Errorf("%s: three threads for four asked passed", name)
		}
		if name == "SwapChain" {
			rs.Heads[1] = roundStateMaxThreads + 1
		} else {
			rs.Pool[1] = roundStateMaxThreads + 1
		}
		if err := CheckRoundState(name, rs, rs, 1); err == nil {
			t.Errorf("%s: a recorded size past the range passed", name)
		}
	}
}

func TestVariableBuffers(t *testing.T) {
	parent, sub := VariableSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(t, mod, VariableEntry)
	if !e.Buffers[0].Type.Dynamic || !e.Buffers[1].Type.Dynamic {
		t.Fatal("variable bindings lost during lowering")
	}
	// The output is twice the input's length, so a thread past the
	// input's end reads the input at the index masked with its
	// length, and every thread adds the input's length to its own
	// word. That is the reference the backends are held to.
	for _, n := range VariableSizes {
		in, out := VariableInputs(n)
		RunVariableGo(in, out)
		for i, word := range out {
			want := (in[i&(n-1)] ^ uint32(n) ^ uint32(len(out)) ^ Probes()[0].A) + uint32(n)
			if word != want {
				t.Fatalf("length %d word %d: %#x, want %#x", n, i, word, want)
			}
		}
	}
}

func TestVariableArray(t *testing.T) {
	parent, sub := VariableSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(t, mod, VariableArrayEntry)
	if len(e.Buffers) != 3 {
		t.Fatalf("array entry binds %d buffers, want the group's two and the output", len(e.Buffers))
	}
	for _, bb := range e.Buffers {
		if !bb.Type.Dynamic || !bb.Type.Word {
			t.Fatalf("array binding %+v, want a dynamic word buffer", bb.Type)
		}
	}
	// Even threads read the first buffer and odd threads the second,
	// each at its half index masked with its own length. That is the
	// reference the backends are held to.
	for _, n := range VariableSizes {
		v, out := VariableArrayInputs(n)
		RunVariableArrayGo(&v, out)
		n0, n1 := uint32(len(v.In[0])), uint32(len(v.In[1]))
		for i, got := range out {
			w := v.In[0][(i>>1)&int(n0-1)]
			if i&1 == 1 {
				w = v.In[1][(i>>1)&int(n1-1)]
			}
			if want := w ^ n0 ^ n1<<1 ^ Probes()[0].A; got != want {
				t.Fatalf("length %d word %d: %#x, want %#x", n, i, got, want)
			}
		}
	}
}

func TestVariableHalfwords(t *testing.T) {
	parent, sub := VariableSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(t, mod, VariableHalfwordEntry)
	for _, bb := range e.Buffers[:2] {
		if !bb.Type.Dynamic || bb.Type.Word {
			t.Fatalf("halfword binding %+v, want a dynamic byte buffer", bb.Type)
		}
	}
	// Thread i reads the input's bytes 2i+1 and 2i+2 masked with the
	// input's length and writes the output's bytes 2i+1 and 2i+2 masked
	// with the output's, the last thread's second byte landing on byte
	// zero. That is the reference the backends are held to.
	for _, n := range VariableSizes {
		in, out := VariableHalfwordInputs(n)
		want := append(compute.VDP1FB(nil), out...)
		RunVariableHalfwordsGo(in, out)
		un, m := uint32(n), uint32(len(out))
		for i := range m / 2 {
			a := 2*i + 1
			v := uint16(in[a&(un-1)])<<8 | uint16(in[(a+1)&(un-1)])
			v ^= uint16(un^un>>16) ^ uint16(m>>1^m>>17) ^ uint16(Probes()[0].A)
			want[a&(m-1)], want[(a+1)&(m-1)] = uint8(v>>8), uint8(v)
		}
		if !bytes.Equal(out, want) {
			t.Fatalf("length %d: halfword output differs from the reference", n)
		}
	}
}

func TestVariableByteBuffers(t *testing.T) {
	parent, sub := VariableSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(t, mod, VariableByteEntry)
	for _, bb := range e.Buffers[:2] {
		if !bb.Type.Dynamic || bb.Type.Word {
			t.Fatalf("byte binding %+v, want a dynamic byte buffer", bb.Type)
		}
	}
	// Thread i reads the input at i masked with the input's length and
	// writes the output at i plus the output's length masked with the
	// output's length, which is its own byte. That is the reference the
	// backends are held to.
	for _, n := range VariableSizes {
		in, out := VariableByteInputs(n)
		RunVariableBytesGo(in, out)
		m := uint32(len(out))
		for i, got := range out {
			want := in[i&(n-1)] ^ uint8(uint32(n)^uint32(n)>>8^uint32(n)>>16) ^ uint8(m>>1^m>>9^m>>17) ^ uint8(Probes()[0].A)
			if got != want {
				t.Fatalf("length %d byte %d: %#x, want %#x", n, i, got, want)
			}
		}
	}
}
