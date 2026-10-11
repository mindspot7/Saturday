// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import "math"

// Lengths fills dst with the word count a backend supplies for each
// bound buffer of an entry with a variable-length buffer: the buffer's
// size over four. A variable-length binding's size is a power of two,
// which CheckBindings holds, so its count is the one the helpers mask
// with. dst holds at least one word per buffer.
func Lengths(dst []uint32, buffers []Buffer) {
	for i, b := range buffers {
		dst[i] = uint32(b.Size() / 4)
	}
}

// CheckBindings is the dispatch binding check a backend makes before
// it binds: the buffer count is the n the entry declares
// (ErrBindingCount), none is a readback buffer, which the GPU writes
// only as a Copy destination (ErrReadbackBound), and each
// variable-length binding, by bit in dynamic, has a power-of-two size
// of at least four bytes, since the helpers mask with its word count
// (ErrVariableWords).
func CheckBindings(buffers []Buffer, n int, dynamic uint32) error {
	if len(buffers) != n {
		return ErrBindingCount
	}
	for i, b := range buffers {
		if b.Kind() == BufferReadback {
			return ErrReadbackBound
		}
		if size := b.Size(); dynamic>>i&1 != 0 && (size < 4 || size&(size-1) != 0) {
			return ErrVariableWords
		}
	}
	return nil
}

// CheckCopy is the copy check a backend makes before it records a
// copy: two distinct buffers (ErrCopySame), a destination the GPU
// writes (ErrCopyUpload) and a source it reads (ErrCopyReadback),
// offsets and a length that are non-negative multiples of four
// (ErrCopyAlign), and ranges inside both buffers (ErrCopyRange), so
// a device never reaches past a buffer.
func CheckCopy(dst Buffer, dstOffset int, src Buffer, srcOffset int, n int) error {
	switch {
	case dst == src:
		return ErrCopySame
	case dst.Kind() == BufferUpload:
		return ErrCopyUpload
	case src.Kind() == BufferReadback:
		return ErrCopyReadback
	case dstOffset < 0 || srcOffset < 0 || n < 0 || (dstOffset|srcOffset|n)&3 != 0:
		return ErrCopyAlign
	case n > dst.Size()-dstOffset || n > src.Size()-srcOffset:
		return ErrCopyRange
	}
	return nil
}

// CheckFill is the fill check a backend makes before it records a
// fill: a working or an output buffer (ErrFillKind), an offset and a
// length that are non-negative multiples of four (ErrFillAlign), and
// a range inside the buffer (ErrFillRange).
func CheckFill(dst Buffer, offset int, n int) error {
	switch {
	case dst.Kind() != BufferWorking && dst.Kind() != BufferOutput:
		return ErrFillKind
	case offset < 0 || n < 0 || (offset|n)&3 != 0:
		return ErrFillAlign
	case n > dst.Size()-offset:
		return ErrFillRange
	}
	return nil
}

// Groups is the workgroups that cover a grid of threads, each
// dimension rounded up to whole workgroups of the pipeline's
// workgroup, the emitter's constant, one or more in each dimension.
// A negative dimension, or one whose rounded-up workgroups do not
// fit 32 bits, is ErrGrid.
func Groups(grid [2]int, workgroup [2]uint32) ([2]uint32, error) {
	var groups [2]uint32
	for i := range grid {
		if grid[i] < 0 || uint64(grid[i]) > math.MaxUint32-uint64(workgroup[i])+1 {
			return groups, ErrGrid
		}
		groups[i] = (uint32(grid[i]) + workgroup[i] - 1) / workgroup[i]
	}
	return groups, nil
}
