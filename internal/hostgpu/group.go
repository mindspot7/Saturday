// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import "math"

// GroupLoad is a group dispatch's work: its ops, a row of the grid
// each, and their chunks, the pieces of an op's work that run
// independently on separate threads. An op's threads take its chunks
// by stride, so a row narrower than an op's chunks loops over them and
// a row wider leaves threads with none.
type GroupLoad struct {
	OpCount   int // number of ops in the dispatch
	MaxChunks int // chunks of the largest op
	SumChunks int // chunks of every op together
}

// GroupWidth is the threads across each row of a group dispatch of the
// load, in whole workgroups of workgroup threads, at most widest (a
// multiple of workgroup, the widest row one dispatch of the backend
// issues). lanes is the threads the device runs at once, or zero when
// the device does not report it.
//
// When every row at the largest op's chunks fits in the device's
// lanes, the rows are that wide: no op loops, and the threads with no
// chunk hold lanes that would be idle. Otherwise the width is the
// geometric mean of the average op's chunks and the largest op's, at
// which the largest op loops sqrt(MaxChunks / average) times and the
// dispatch runs that many times the threads its chunks need, so
// neither grows past the other for any mix of op sizes.
func GroupWidth(load GroupLoad, lanes, workgroup, widest int) int {
	ops := max(load.OpCount, 1)
	most := max(load.MaxChunks, 1)
	var w int
	if lanes > 0 && int64(ops)*int64(most) <= int64(lanes) {
		w = most
	} else {
		avg := min(max((load.SumChunks+ops-1)/ops, 1), most)
		w = int(math.Ceil(math.Sqrt(float64(avg) * float64(most))))
	}
	w = (w + workgroup - 1) / workgroup * workgroup
	return min(max(w, workgroup), widest)
}
