// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// BusRegion describes one region of the console's native address bus
// in canonical addresses.
type BusRegion struct {
	Name  string
	Start uint32 // canonical native bus address of the region's first byte
	Size  uint32 // region size in bytes
}

// busTable is the access gate for external memory access.
var busTable = []BusRegion{
	{Name: "wraml", Start: 0x00200000, Size: wramLSize},
	{Name: "wramh", Start: 0x06000000, Size: wramHSize},
}

// Regions returns the accessible bus regions.
func (e *Emulator) Regions() []BusRegion {
	out := make([]BusRegion, len(busTable))
	copy(out, busTable)
	return out
}

// busClamp validates addr against the region table and returns how
// many of want bytes fit before the region's end, or false when addr
// is outside every region.
func busClamp(addr, want uint32) (uint32, bool) {
	for i := range busTable {
		r := &busTable[i]
		if addr >= r.Start && addr-r.Start < r.Size {
			if remaining := r.Size - (addr - r.Start); want > remaining {
				want = remaining
			}
			return want, true
		}
	}
	return 0, false
}

// ReadMemory reads guest RAM at a canonical native bus address into
// buf and returns the number of bytes read. Bytes come through the bus
// decode, so external reads see exactly what the emulated CPUs see.
// Reads clamp at the region end; addresses outside the canonical
// regions read nothing. Calls happen between frames with the emulation
// threads parked, so the lock-free and trace-free bus access is used.
func (e *Emulator) ReadMemory(addr uint32, buf []byte) uint32 {
	n, ok := busClamp(addr, uint32(len(buf)))
	if !ok {
		return 0
	}
	for i := uint32(0); i < n; i++ {
		buf[i] = e.bus.read8Impl(addr + i)
	}
	return n
}

// WriteMemory writes data to guest RAM at a canonical native bus
// address and returns the number of bytes written. Bytes go through
// the bus decode, matching ReadMemory, and reach the master SH-2's
// cache as its own store would (externalWrite8). Writes clamp at the
// region end; addresses outside the canonical regions write nothing.
func (e *Emulator) WriteMemory(addr uint32, data []byte) uint32 {
	n, ok := busClamp(addr, uint32(len(data)))
	if !ok {
		return 0
	}
	for i := uint32(0); i < n; i++ {
		e.externalWrite8(addr+i, data[i])
	}
	return n
}

// externalWrite8 writes one byte of guest RAM as a store by the master
// SH-2 would: memory through the bus decode, and the master's cache line
// when one holds the address (write-through write hit; nothing is
// allocated). The slave's cache is not updated, as on hardware, where
// neither CPU snoops the other's stores. Calls happen between frames
// with the emulation threads parked.
func (e *Emulator) externalWrite8(addr uint32, val uint8) {
	e.bus.write8Impl(addr, val)
	e.master.CacheStoreHit8(addr, val)
}

// ReadMemoryFlat reads from the Saturn flat memory convention into buf
// and returns the number of bytes read. The flat layout matches the
// rcheevos Saturn memory map: 0x000000-0x0FFFFF is Work RAM-L
// (hardware 0x00200000) and 0x100000-0x1FFFFF is Work RAM-H (hardware
// 0x06000000). Reads stop at the first address outside that range.
// The switch maps flat offsets to native addresses only; the bus
// decode selects every byte, matching ReadMemory.
func (e *Emulator) ReadMemoryFlat(off uint32, buf []byte) uint32 {
	var n uint32
	for i := range buf {
		cur := off + uint32(i)
		switch {
		case cur < wramLSize:
			buf[i] = e.bus.read8Impl(0x00200000 + cur)
		case cur < wramLSize+wramHSize:
			buf[i] = e.bus.read8Impl(0x06000000 + (cur - wramLSize))
		default:
			return n
		}
		n++
	}
	return n
}

// WriteMemoryFlat writes data to the Saturn flat memory convention and
// returns the number of bytes written, matching ReadMemoryFlat's
// layout and boundary behavior. The switch maps flat offsets to native
// addresses only; each byte is written as WriteMemory writes it.
func (e *Emulator) WriteMemoryFlat(off uint32, data []byte) uint32 {
	var n uint32
	for i := range data {
		cur := off + uint32(i)
		switch {
		case cur < wramLSize:
			e.externalWrite8(0x00200000+cur, data[i])
		case cur < wramLSize+wramHSize:
			e.externalWrite8(0x06000000+(cur-wramLSize), data[i])
		default:
			return n
		}
		n++
	}
	return n
}
