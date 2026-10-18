// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

func TestApplyRAMCheatWritesWorkRAM(t *testing.T) {
	e := NewEmulator()
	if err := e.ApplyRAMCheat(0x00200010, 0x7F, 1); err != nil {
		t.Fatalf("write byte: %v", err)
	}
	if got := e.bus.wramL[0x10]; got != 0x7F {
		t.Fatalf("WRAM-L byte = %02X, want 7F", got)
	}
	if err := e.ApplyRAMCheat(0x06000020, 0x1234, 2); err != nil {
		t.Fatalf("write word: %v", err)
	}
	if got := e.bus.wramH[0x20]; got != 0x12 {
		t.Fatalf("WRAM-H word high byte = %02X, want 12", got)
	}
	if got := e.bus.wramH[0x21]; got != 0x34 {
		t.Fatalf("WRAM-H word low byte = %02X, want 34", got)
	}
	if err := e.ApplyRAMCheat(0x00200030, 0x12345678, 4); err != nil {
		t.Fatalf("write longword: %v", err)
	}
	if got := e.bus.wramL[0x30]; got != 0x12 {
		t.Fatalf("WRAM-L longword first byte = %02X, want 12", got)
	}
	if got := e.bus.wramL[0x33]; got != 0x78 {
		t.Fatalf("WRAM-L longword last byte = %02X, want 78", got)
	}
}

func TestApplyRAMCheatRejectsUnsafeWrites(t *testing.T) {
	e := NewEmulator()
	for _, c := range []struct {
		name string
		addr uint32
		val  uint32
		size int
	}{
		{"BIOS", 0x00000200, 0x12, 1},
		{"hardware register", 0x05FE0000, 0x1234, 2},
		{"unsupported width", 0x00200000, 0x12, 3},
		{"unaligned word", 0x00200001, 0x1234, 2},
		{"RAM boundary", 0x002FFFFE, 0x12345678, 4},
		{"mirrored RAM boundary", 0x061FFFFE, 0x12345678, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := e.ApplyRAMCheat(c.addr, c.val, c.size); err == nil {
				t.Fatalf("ApplyRAMCheat(%08X, %X, %d) unexpectedly succeeded", c.addr, c.val, c.size)
			}
		})
	}
}
