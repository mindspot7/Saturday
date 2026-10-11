// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import (
	"errors"
	"math"
	"slices"
	"testing"
)

// sizedBuffer is a buffer that reports a size and a kind and nothing
// else. Each is its own buffer, as a backend's are, whatever its size.
type sizedBuffer struct {
	size int
	kind BufferKind
}

func (b *sizedBuffer) Bytes() []byte    { return nil }
func (b *sizedBuffer) Size() int        { return b.size }
func (b *sizedBuffer) Kind() BufferKind { return b.kind }
func (b *sizedBuffer) Release()         {}

// working is a working buffer of size bytes.
func working(size int) Buffer { return &sizedBuffer{size: size, kind: BufferWorking} }

// TestLengths checks the word counts for a binding list: each
// buffer's size over four.
func TestLengths(t *testing.T) {
	buffers := []Buffer{working(64), working(24), working(1 << 20), working(12)}
	dst := make([]uint32, len(buffers)+1)
	Lengths(dst, buffers)
	if want := []uint32{16, 6, 1 << 18, 3, 0}; !slices.Equal(dst, want) {
		t.Errorf("lengths %v, want %v", dst, want)
	}
}

// refuses holds that f reports want.
func refuses(t *testing.T, name string, want error, f func() error) {
	t.Helper()
	if err := f(); !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", name, err, want)
	}
}

// admits holds that f reports nothing.
func admits(t *testing.T, name string, f func() error) {
	t.Helper()
	if err := f(); err != nil {
		t.Errorf("%s: refused: %v", name, err)
	}
}

// TestCheckBindings checks that a dispatch binds upload and working
// buffers at the entry's count, refuses a readback buffer and a
// count other than the entry's, and holds a variable-length binding
// to a power-of-two word count.
func TestCheckBindings(t *testing.T) {
	up := &sizedBuffer{size: 16, kind: BufferUpload}
	back := &sizedBuffer{size: 16, kind: BufferReadback}
	out := &sizedBuffer{size: 16, kind: BufferOutput}
	admits(t, "upload, working, output", func() error { return CheckBindings([]Buffer{up, working(16), out}, 3, 0) })
	refuses(t, "a readback binding", ErrReadbackBound, func() error { return CheckBindings([]Buffer{up, back}, 2, 0) })
	refuses(t, "one buffer short", ErrBindingCount, func() error { return CheckBindings([]Buffer{up}, 2, 0) })
	refuses(t, "one buffer over", ErrBindingCount, func() error { return CheckBindings([]Buffer{up, out}, 1, 0) })
	for _, words := range []int{1, 2, 64, 1 << 20} {
		admits(t, "variable-length binding", func() error { return CheckBindings([]Buffer{working(words * 4)}, 1, 1) })
	}
	for _, size := range []int{0, 2, 6, 12, 24, 4*64 + 4} {
		admits(t, "fixed binding of any size", func() error { return CheckBindings([]Buffer{working(size)}, 1, 0) })
		refuses(t, "variable-length binding of an odd size", ErrVariableWords, func() error { return CheckBindings([]Buffer{working(64), working(size)}, 2, 0b10) })
	}
}

// TestCheckCopy checks the copies the contract admits and the ones it
// refuses: the same buffer, a copy into an upload buffer or from a
// readback buffer, a negative or unaligned offset or length, and a
// range past either buffer, including one whose end overflows.
func TestCheckCopy(t *testing.T) {
	a, b := working(64), working(32)
	up := &sizedBuffer{size: 64, kind: BufferUpload}
	back := &sizedBuffer{size: 64, kind: BufferReadback}
	admitted := []struct {
		name              string
		dst, src          Buffer
		dstOff, srcOff, n int
	}{
		{"whole", a, b, 0, 0, 32},
		{"to the end", a, b, 32, 0, 32},
		{"last word", b, a, 28, 60, 4},
		{"empty at the end", a, b, 64, 32, 0},
		{"from an upload buffer", a, up, 0, 0, 64},
		{"into a readback buffer", back, a, 0, 0, 64},
		{"upload to readback", back, up, 0, 0, 64},
	}
	for _, c := range admitted {
		admits(t, c.name, func() error { return CheckCopy(c.dst, c.dstOff, c.src, c.srcOff, c.n) })
	}
	refuses(t, "into an upload buffer", ErrCopyUpload, func() error { return CheckCopy(up, 0, a, 0, 4) })
	refuses(t, "from a readback buffer", ErrCopyReadback, func() error { return CheckCopy(a, 0, back, 0, 4) })
	refused := []struct {
		name              string
		dst               Buffer
		dstOff, srcOff, n int
		want              error
	}{
		{"same buffer", b, 0, 0, 4, ErrCopySame},
		{"unaligned destination", a, 2, 0, 4, ErrCopyAlign},
		{"unaligned source", a, 0, 2, 4, ErrCopyAlign},
		{"unaligned length", a, 0, 0, 6, ErrCopyAlign},
		{"negative offset", a, -4, 0, 4, ErrCopyAlign},
		{"negative length", a, 0, 0, -4, ErrCopyAlign},
		{"past the destination", a, 36, 0, 32, ErrCopyRange},
		{"past the source", a, 0, 4, 32, ErrCopyRange},
		{"overflowing end", a, 0, 4, 1<<62 + 4, ErrCopyRange},
	}
	for _, r := range refused {
		refuses(t, r.name, r.want, func() error { return CheckCopy(r.dst, r.dstOff, b, r.srcOff, r.n) })
	}
}

// TestCheckFill checks the fills the contract admits and the ones it
// refuses: a buffer other than a working or an output buffer, a
// negative or unaligned offset or length, and a range past the buffer,
// including one whose end overflows.
func TestCheckFill(t *testing.T) {
	b := working(64)
	admits(t, "whole", func() error { return CheckFill(b, 0, 64) })
	admits(t, "last word", func() error { return CheckFill(b, 60, 4) })
	admits(t, "empty at the end", func() error { return CheckFill(b, 64, 0) })
	admits(t, "output buffer", func() error { return CheckFill(&sizedBuffer{size: 64, kind: BufferOutput}, 0, 64) })
	refuses(t, "an upload buffer", ErrFillKind, func() error { return CheckFill(&sizedBuffer{size: 64, kind: BufferUpload}, 0, 4) })
	refuses(t, "a readback buffer", ErrFillKind, func() error { return CheckFill(&sizedBuffer{size: 64, kind: BufferReadback}, 0, 4) })
	refused := []struct {
		name      string
		offset, n int
		want      error
	}{
		{"unaligned offset", 2, 4, ErrFillAlign},
		{"unaligned length", 0, 6, ErrFillAlign},
		{"negative offset", -4, 4, ErrFillAlign},
		{"negative length", 0, -4, ErrFillAlign},
		{"past the end", 32, 36, ErrFillRange},
		{"offset past the end", 68, 0, ErrFillRange},
		{"overflowing end", 4, 1<<62 + 4, ErrFillRange},
	}
	for _, r := range refused {
		refuses(t, r.name, r.want, func() error { return CheckFill(b, r.offset, r.n) })
	}
}

// TestGroups checks the rounding of a grid to whole workgroups, at
// exact and inexact sizes in each dimension, and the grids it
// refuses: a negative dimension and one whose rounded-up workgroups
// do not fit 32 bits.
func TestGroups(t *testing.T) {
	cases := []struct {
		grid      [2]int
		workgroup [2]uint32
		want      [2]uint32
	}{
		{[2]int{64, 1}, [2]uint32{64, 1}, [2]uint32{1, 1}},
		{[2]int{65, 1}, [2]uint32{64, 1}, [2]uint32{2, 1}},
		{[2]int{1, 1}, [2]uint32{128, 1}, [2]uint32{1, 1}},
		{[2]int{704, 512}, [2]uint32{8, 8}, [2]uint32{88, 64}},
		{[2]int{705, 513}, [2]uint32{8, 8}, [2]uint32{89, 65}},
		{[2]int{0, 0}, [2]uint32{8, 8}, [2]uint32{0, 0}},
		{[2]int{math.MaxUint32 - 7, 1}, [2]uint32{8, 1}, [2]uint32{1<<29 - 1, 1}},
	}
	bad := []struct {
		name string
		grid [2]int
	}{
		{"negative width", [2]int{-1, 1}},
		{"negative rows", [2]int{1, -1}},
		{"width past 32 bits", [2]int{math.MaxUint32 - 6, 1}},
		{"rows past 32 bits", [2]int{1, math.MaxUint32 + 1}},
	}
	for _, c := range cases {
		got, err := Groups(c.grid, c.workgroup)
		if err != nil || got != c.want {
			t.Errorf("grid %v workgroup %v: %v, %v, want %v", c.grid, c.workgroup, got, err, c.want)
		}
	}
	for _, r := range bad {
		if _, err := Groups(r.grid, [2]uint32{8, 1}); !errors.Is(err, ErrGrid) {
			t.Errorf("%s: %v, want %v", r.name, err, ErrGrid)
		}
	}
}
