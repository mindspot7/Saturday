// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
)

// TestVDP1RecordsGrow preserves earlier reservations when many ops
// reserve whole sprites but never execute their lines (manual restarts).
func TestVDP1RecordsGrow(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.lineRecords = make(compute.LineRecords, 4)
	var bases []uint32
	run := func() {
		bases = bases[:0]
		for i := 0; i < 100; i++ {
			op := draw.DrawOp{Kind: draw.OpNormalSprite, CharW: 8, CharH: 32}
			v.reserveRecords(&op)
			bases = append(bases, op.RecordBase)
			v.lineRecords[op.RecordBase] = uint32(i + 1)
			v.lineRecords[op.RecordBase+31] = uint32(1000 + i)
		}
	}
	run()
	if len(bases) != 100 || v.recordWords != 3200 {
		t.Fatal("lost reservations")
	}
	for i, base := range bases {
		if v.lineRecords[base] != uint32(i+1) || v.lineRecords[base+31] != uint32(1000+i) {
			t.Fatalf("op %d records overwritten", i)
		}
	}
	capacity := len(v.lineRecords)
	if n := testing.AllocsPerRun(10, func() { v.resetRecords(); run() }); n != 0 {
		t.Fatalf("steady workload allocated %g times", n)
	}
	if len(v.lineRecords) != capacity {
		t.Fatal("reset discarded capacity")
	}
}

// TestVDP1RecordResetReservesResume keeps the active op's records
// across a reset, moves its range to the front of the buffer, and
// holds it against further reservations and growth.
func TestVDP1RecordResetReservesResume(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.lineRecords = make(compute.LineRecords, 8)
	first := draw.DrawOp{Kind: draw.OpNormalSprite, CharW: 8, CharH: 4}
	v.reserveRecords(&first)
	v.cmd.op = first
	v.cmd.op.RecordBase = 4
	v.cmdPhase = phaseDraw
	v.lineRecords[4] = 0x1234
	v.resetRecords()
	if v.recordWords != 4 || v.cmd.op.RecordBase != 0 {
		t.Fatalf("reset reserved %d words at base %d, want 4 at 0", v.recordWords, v.cmd.op.RecordBase)
	}
	if v.lineRecords[0] != 0x1234 {
		t.Fatal("reset lost the active op's records")
	}
	op := draw.DrawOp{Kind: draw.OpNormalSprite, CharW: 8, CharH: 32}
	v.reserveRecords(&op)
	if op.RecordBase != 4 || v.lineRecords[0] != 0x1234 {
		t.Fatal("new op overlapped resumed records")
	}
	v.lineRecords[v.cmd.op.RecordBase+3] = 0x5678
	if v.lineRecords[3] != 0x5678 {
		t.Fatal("resume did not use its moved range")
	}
	v.cmdPhase = phaseIdle
	v.resetRecords()
	if v.recordWords != 0 {
		t.Fatal("completed reservation not reclaimed")
	}
}

// TestVDP1RecordResetReclaimsPrefix runs frames that each carry a
// command over the reset. The carried command's range moves to the
// front, so every frame reserves the same words and the buffer never
// grows; leaving the range where it was would strand the finished
// ops' storage below it and climb one frame's worth each frame.
func TestVDP1RecordResetReclaimsPrefix(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.lineRecords = make(compute.LineRecords, 64)
	for frame := 0; frame < 8; frame++ {
		v.resetRecords()
		if frame > 0 && v.lineRecords[v.cmd.op.RecordBase] != 0xFEED {
			t.Fatalf("frame %d lost the carried command's records", frame)
		}
		for i := 0; i < 4; i++ {
			op := draw.DrawOp{Kind: draw.OpNormalSprite, CharW: 8, CharH: 8}
			v.reserveRecords(&op)
			if i == 3 {
				// The last op is still drawing when the frame ends.
				v.cmd.op = op
				v.cmdPhase = phaseDraw
				v.lineRecords[op.RecordBase] = 0xFEED
			}
		}
		want := uint32(40)
		if frame == 0 {
			want = 32
		}
		if v.recordWords != want {
			t.Fatalf("frame %d reserved %d words, want %d", frame, v.recordWords, want)
		}
	}
	if len(v.lineRecords) != 64 {
		t.Fatalf("the buffer grew to %d words", len(v.lineRecords))
	}
}

// TestVDP1RecordGrowthDuringDraw grows the record buffer while a
// sprite is part way through its rows and checks the resumed draw
// matches an uninterrupted one.
func TestVDP1RecordGrowthDuringDraw(t *testing.T) {
	v := benchVDP1Scenes()[0].build()
	want := benchVDP1Scenes()[0].build()
	v.lineRecords = make(compute.LineRecords, 32)
	for _, chip := range []*VDP1{v, want} {
		chip.VBlankIn()
		chip.VBlankOut()
		chip.StartAutoDraw()
	}
	// Ticks small enough to stop inside a sprite's draw.
	const tick = 10
	for ticks := 0; ; ticks++ {
		if ticks == 1000 {
			t.Fatal("did not reach a partially drawn sprite")
		}
		v.TickSystemCycles(tick)
		want.TickSystemCycles(tick)
		if v.cmdPhase == phaseDraw && v.cmd.st.Line > 0 {
			break
		}
	}
	first := v.lineRecords[v.cmd.op.RecordBase]
	v.resetRecords()
	extra := draw.DrawOp{Kind: draw.OpNormalSprite, CharW: 8, CharH: 255}
	v.reserveRecords(&extra)
	if len(v.lineRecords) <= 32 || v.lineRecords[v.cmd.op.RecordBase] != first {
		t.Fatal("growth lost the running sprite's records")
	}
	v.TickSystemCycles(benchVDP1Budget)
	want.TickSystemCycles(benchVDP1Budget)
	equalRenderBytes(t, "resumed draw buffer", v.drawFB, want.drawFB)
	equalRenderBytes(t, "resumed display buffer", v.displayFB, want.displayFB)
}

// TestVDP1FrameNoAllocs pins zero allocations per frame for every
// benchmark scene.
func TestVDP1FrameNoAllocs(t *testing.T) {
	for _, s := range benchVDP1Scenes() {
		v := s.build()
		frame := func() { benchVDP1Frame(v) }
		frame()
		if n := testing.AllocsPerRun(5, frame); n != 0 {
			t.Errorf("%s: %v allocations per frame", s.name, n)
		}
	}
}
