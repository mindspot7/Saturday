// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import "testing"

// TestGroupWidth holds the group row width: the largest op's chunks
// when every row at that width fits the device's lanes, the geometric
// mean of the average and the largest op's chunks otherwise or when
// the device reports no lanes, in whole workgroups, at least one
// workgroup and at most the widest row.
func TestGroupWidth(t *testing.T) {
	const widest = 65535 * 128
	for _, c := range []struct {
		name  string
		load  GroupLoad
		lanes int
		want  int
	}{
		{"one erase", GroupLoad{1, 1792, 1792}, 0, 1792},
		{"equal ops", GroupLoad{100, 256, 25600}, 0, 256},
		{"one large among small", GroupLoad{100, 8192, 11360}, 0, 1024},
		{"one very large among many small", GroupLoad{1000, 65536, 97504}, 0, 2560},
		{"fits the lanes", GroupLoad{2, 1000, 1500}, 3328, 1024},
		{"past the lanes", GroupLoad{2, 1000, 1500}, 1024, 896},
		{"under a workgroup", GroupLoad{1, 10, 10}, 0, 128},
		{"past the widest row", GroupLoad{1, 1 << 30, 1 << 30}, 0, widest},
		{"no ops", GroupLoad{}, 0, 128},
		{"no chunks summed", GroupLoad{4, 100, 0}, 0, 128},
	} {
		if got := GroupWidth(c.load, c.lanes, 128, widest); got != c.want {
			t.Errorf("%s: %d threads, want %d", c.name, got, c.want)
		}
	}
}
