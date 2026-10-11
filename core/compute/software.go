// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

// The software renderer's forms of the operations Operations lists, the
// operations a compute function cannot write as plain Go. Every
// function in this file is one of them, by name: the compile stage
// never compiles a body below, it calls the GPU renderer's backend
// for the operation instead, and the validator holds every function
// declared in this package to an operation in Operations. The bodies
// are what the software renderer runs, one thread per dispatch, and
// the single-thread reference the conformance rules hold every
// backend to.
//
// Load, Store, Length, and the Min and Max operations have no
// function here: their software forms are Go's own index, indexed
// store, len, min, and max, which the compile stage lowers to the
// operations.

// Byte buffers are read and written through accessors, since an
// access has a width and the Saturn's byte order: multi-byte accesses
// are big-endian, as its memory is, and every byte of an access is
// masked to the buffer's size individually, so an access straddling
// the end of the buffer wraps as the hardware address does. A backend
// whose buffers hold words reads the word holding the bytes, and
// writes only the bytes of the access.

func (b *VDP2VRAM) U8(a uint32) uint8 { return b[a&(VDP2VRAMSize-1)] }

func (b *VDP2VRAM) U16BE(a uint32) uint16 {
	return uint16(b[a&(VDP2VRAMSize-1)])<<8 | uint16(b[(a+1)&(VDP2VRAMSize-1)])
}

func (b *VDP2VRAM) U32BE(a uint32) uint32 { return uint32(b.U16BE(a))<<16 | uint32(b.U16BE(a+2)) }

func (b *VDP2VRAM) PutU8(a uint32, v uint8) { b[a&(VDP2VRAMSize-1)] = v }

func (b *VDP2VRAM) PutU16BE(a uint32, v uint16) {
	b[a&(VDP2VRAMSize-1)], b[(a+1)&(VDP2VRAMSize-1)] = uint8(v>>8), uint8(v)
}

func (b *VDP2VRAM) PutU32BE(a uint32, v uint32) {
	b.PutU16BE(a, uint16(v>>16))
	b.PutU16BE(a+2, uint16(v))
}

func (b *CRAM) U16BE(a uint32) uint16 {
	return uint16(b[a&(CRAMSize-1)])<<8 | uint16(b[(a+1)&(CRAMSize-1)])
}

func (b *CRAM) PutU32BE(a uint32, v uint32) {
	b[a&(CRAMSize-1)], b[(a+1)&(CRAMSize-1)] = uint8(v>>24), uint8(v>>16)
	b[(a+2)&(CRAMSize-1)], b[(a+3)&(CRAMSize-1)] = uint8(v>>8), uint8(v)
}

func (b *VDP1VRAM) U8(a uint32) uint8 { return b[a&(VDP1VRAMSize-1)] }

func (b *VDP1VRAM) U16BE(a uint32) uint16 {
	return uint16(b[a&(VDP1VRAMSize-1)])<<8 | uint16(b[(a+1)&(VDP1VRAMSize-1)])
}

func (b *VDP1VRAM) PutU32BE(a uint32, v uint32) {
	b[a&(VDP1VRAMSize-1)], b[(a+1)&(VDP1VRAMSize-1)] = uint8(v>>24), uint8(v>>16)
	b[(a+2)&(VDP1VRAMSize-1)], b[(a+3)&(VDP1VRAMSize-1)] = uint8(v>>8), uint8(v)
}

// A variable-length byte buffer's accessors mask with its length
// minus one, a power of two, as a backend's form does.

func (b VDP1FB) U8(a uint32) uint8 {
	m := len(b) - 1
	if m < 0 {
		return 0
	}
	return b[int(a)&m]
}

func (b VDP1FB) U16BE(a uint32) uint16 {
	m := len(b) - 1
	if m < 0 {
		return 0
	}
	return uint16(b[int(a)&m])<<8 | uint16(b[int(a+1)&m])
}

func (b VDP1FB) PutU8(a uint32, v uint8) {
	m := len(b) - 1
	if m < 0 {
		return
	}
	b[int(a)&m] = v
}

func (b VDP1FB) PutU16BE(a uint32, v uint16) {
	m := len(b) - 1
	if m < 0 {
		return
	}
	b[int(a)&m], b[int(a+1)&m] = uint8(v>>8), uint8(v)
}

func (b LineColor) U8(a uint32) uint8 {
	m := len(b) - 1
	if m < 0 {
		return 0
	}
	return b[int(a)&m]
}

func (b LineColor) PutU8(a uint32, v uint8) {
	m := len(b) - 1
	if m < 0 {
		return
	}
	b[int(a)&m] = v
}

func (b *PageStore) U32BE(a uint32) uint32 {
	return uint32(b[a&(PageStoreBytes-1)])<<24 | uint32(b[(a+1)&(PageStoreBytes-1)])<<16 |
		uint32(b[(a+2)&(PageStoreBytes-1)])<<8 | uint32(b[(a+3)&(PageStoreBytes-1)])
}

func (b *CRAMPageStore) U32BE(a uint32) uint32 {
	return uint32(b[a&(CRAMPageStoreBytes-1)])<<24 | uint32(b[(a+1)&(CRAMPageStoreBytes-1)])<<16 |
		uint32(b[(a+2)&(CRAMPageStoreBytes-1)])<<8 | uint32(b[(a+3)&(CRAMPageStoreBytes-1)])
}

func (b *VDP1PageStore) U8(a uint32) uint8 { return b[a&(VDP1PageStoreBytes-1)] }

func (b *VDP1PageStore) U32BE(a uint32) uint32 {
	return uint32(b[a&(VDP1PageStoreBytes-1)])<<24 | uint32(b[(a+1)&(VDP1PageStoreBytes-1)])<<16 |
		uint32(b[(a+2)&(VDP1PageStoreBytes-1)])<<8 | uint32(b[(a+3)&(VDP1PageStoreBytes-1)])
}

// The round state's regions each carry the shared-word operation its
// role needs and no other: Raise on the order stamps, Swap on the
// fragment list heads, and Add on the fragment pool. Each masks its
// index with the region's length, a power of two, as a backend's
// form does.

// Raise raises word i to v when v is greater. The threads of a
// dispatch raising one word leave it at the greatest of their
// values in whatever order the backend runs them, and no thread
// learns the order, since nothing is returned. A compute function
// decides which of several dots on a pixel came last with it, by a
// key that carries the order. The software renderer runs a dispatch
// as one thread, so this body is the whole operation there; a GPU
// backend substitutes the device's atomic maximum.
func (w OrderStamps) Raise(i uint32, v uint32) {
	m := len(w) - 1
	if m < 0 {
		return
	}
	if v > w[int(i)&m] {
		w[int(i)&m] = v
	}
}

// Swap stores v into word i and returns the word's prior value. The
// threads of a dispatch swapping into one word each take the value
// the one before it stored, so a compute function links itself at
// the head of a list with it. The software renderer runs a dispatch
// as one thread, so this body is the whole operation there; a GPU
// backend substitutes the device's atomic exchange.
func (w FragmentHeads) Swap(i uint32, v uint32) uint32 {
	m := len(w) - 1
	if m < 0 {
		return 0
	}
	old := w[int(i)&m]
	w[int(i)&m] = v
	return old
}

// Add adds v to word i and returns the word's prior value. The
// threads of a dispatch adding to one word each take a distinct
// prior and leave the word at their sum, in whatever order the
// backend runs them, so a compute function reserves a range of a
// stream with it. The prior is used for addressing and never fed to
// another Add. The software renderer runs a dispatch as one thread,
// so this body is the whole operation there; a GPU backend
// substitutes the device's atomic add.
func (w FragmentPool) Add(i uint32, v uint32) uint32 {
	m := len(w) - 1
	if m < 0 {
		return 0
	}
	old := w[int(i)&m]
	w[int(i)&m] = old + v
	return old
}

// NextPhase marks the point where a group's next phase begins: every
// buffer store a thread of the group made before it is visible to
// every thread of the group after it. A group entry calls it between
// the phases whose stores the next phase reads, in uniform control
// flow, so every thread reaches it. The software renderer runs a
// group as one thread that executes the phases in order, so nothing
// happens here; a GPU backend substitutes the device's group
// barrier.
func NextPhase() {}
