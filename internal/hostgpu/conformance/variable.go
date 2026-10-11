// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package conformance

import (
	"github.com/user-none/erings/core/compute"
	rules "github.com/user-none/erings/core/compute/conformance"
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// VariableEntry names the variable word buffer conformance entry,
// VariableByteEntry and VariableHalfwordEntry the variable byte
// buffer ones, and VariableArrayEntry the array of variable word
// buffers one.
const (
	VariableEntry         = "VariableWords"
	VariableByteEntry     = "VariableBytes"
	VariableHalfwordEntry = "VariableHalfwords"
	VariableArrayEntry    = "VariableArray"
)

// VariableEntries names the variable-buffer conformance entries.
var VariableEntries = [...]string{VariableHalfwordEntry, VariableArrayEntry, VariableByteEntry, VariableEntry}

// VariableSources builds the variable-buffer conformance entries.
func VariableSources() (parent, sub compile.Source) {
	parent, sub = Sources()
	sub.Entries = append(sub.Entries, VariableEntries[:]...)
	return
}

// VariableSizes covers input lengths from a few words to a buffer
// past the record reservation the VDP1 starts a frame with, so no
// mask of a fixed size can stand in for the bound the binding
// supplies. Each is a power of two, as every dynamic length is. The
// byte entry takes the same numbers as byte lengths, each a whole
// number of words.
var VariableSizes = [...]int{4, 256, 1 << 21}

// VariableInputs makes the entry's two views: the output is twice
// the input's length, so the threads past the input's end read it
// at the index masked with its length. Both start filled, so a
// dropped access shows as a word that kept its fill.
func VariableInputs(n int) (in, out compute.LineRecords) {
	in, out = make(compute.LineRecords, n), make(compute.LineRecords, 2*n)
	for i := range in {
		in[i] = uint32(i) * 0x9E3779B9
	}
	for i := range out {
		out[i] = 0xA5A5A5A5
	}
	return
}

// RunVariableGo is the Go reference every backend's variable-buffer
// test compares with: one thread per output word, the wrapper's bound.
func RunVariableGo(in, out compute.LineRecords) {
	p := Probes()[0]
	for i := range uint32(len(out)) {
		rules.VariableWords(in, out, &p, i)
	}
}

// DeviceInputs makes the views of a round trip through a working
// buffer by the variable word entry: the input of n words, the working
// buffer's of 2n, and the output of 4n, the last two filled, so a
// dropped write shows as a word that kept its fill.
func DeviceInputs(n int) (in, mid, out compute.LineRecords) {
	in, mid = VariableInputs(n)
	out = make(compute.LineRecords, 4*n)
	for i := range out {
		out[i] = 0xA5A5A5A5
	}
	return
}

// RunDeviceGo is the Go reference every backend's working buffer test
// compares with: the variable word entry from in into mid, then from
// mid into out.
func RunDeviceGo(in, mid, out compute.LineRecords) {
	RunVariableGo(in, mid)
	RunVariableGo(mid, out)
}

// VariableArrayInputs makes the array entry's buffers: the first of n
// words, the second of 4n, with different data, and the output of 8n
// words filled, so threads past the first buffer's end read it
// masked and a dropped write shows as a word that kept its fill.
func VariableArrayInputs(n int) (v rules.Views, out compute.LineRecords) {
	v.In[0], v.In[1] = make(compute.LineRecords, n), make(compute.LineRecords, 4*n)
	for i := range v.In[0] {
		v.In[0][i] = uint32(i) * 0x9E3779B9
	}
	for i := range v.In[1] {
		v.In[1][i] = ^uint32(i) * 0x85EBCA6B
	}
	out = make(compute.LineRecords, 8*n)
	for i := range out {
		out[i] = 0xA5A5A5A5
	}
	return
}

// RunVariableArrayGo is the Go reference every backend's array test
// compares with: one thread per output word.
func RunVariableArrayGo(v *rules.Views, out compute.LineRecords) {
	p := Probes()[0]
	for i := range uint32(len(out)) {
		rules.VariableArray(v, out, &p, i)
	}
}

// VariableByteInputs makes the byte entry's two buffers of n and 2n
// bytes: the input a byte pattern, the output filled, so a dropped
// write shows as a byte that kept its fill.
func VariableByteInputs(n int) (in, out compute.LineColor) {
	in, out = make(compute.LineColor, n), make(compute.LineColor, 2*n)
	for i := range in {
		in[i] = uint8(i*0x9D + i>>8)
	}
	for i := range out {
		out[i] = 0xA5
	}
	return
}

// VariableHalfwordInputs makes the halfword entry's two buffers of n
// and 2n bytes: the input a byte pattern, the output filled, so a
// dropped write shows as a byte that kept its fill.
func VariableHalfwordInputs(n int) (in, out compute.VDP1FB) {
	in, out = make(compute.VDP1FB, n), make(compute.VDP1FB, 2*n)
	for i := range in {
		in[i] = uint8(i*0x6B + i>>8)
	}
	for i := range out {
		out[i] = 0x5A
	}
	return
}

// RunVariableHalfwordsGo is the Go reference every backend's variable
// halfword test compares with: one thread per output halfword.
func RunVariableHalfwordsGo(in, out compute.VDP1FB) {
	p := Probes()[0]
	for i := range uint32(len(out) / 2) {
		rules.VariableHalfwords(in, out, &p, i)
	}
}

// RunVariableBytesGo is the Go reference every backend's variable
// byte buffer test compares with: one thread per output byte.
func RunVariableBytesGo(in, out compute.LineColor) {
	p := Probes()[0]
	for i := range uint32(len(out)) {
		rules.VariableBytes(in, out, &p, i)
	}
}
