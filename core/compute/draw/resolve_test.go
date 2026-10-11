// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestResolveOrdersLongLists appends fragment lists of 1 to 200
// destination-reading dots to a pixel in a shuffled arrival order,
// with keys repeated, keys at or below the pixel's replace winner,
// and half-transparent and shadow modes, and holds Resolve's result
// to the dots applied in key order, one per key, above the winner.
func TestResolveOrdersLongLists(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const pixel = uint32(37)
	for _, n := range []int{1, 2, 31, 32, 33, 64, 65, 100, 200} {
		for trial := 0; trial < 20; trial++ {
			rs := &compute.RoundState{
				Stamps: make(compute.OrderStamps, 64),
				Heads:  make(compute.FragmentHeads, 64),
				Pool:   make(compute.FragmentPool, 4096),
			}
			fb := make(compute.VDP1FB, 256)
			start := uint16(0x8000 | uint32(rng.Intn(1<<15)))
			fb.PutU16BE(pixel*2, start)
			winner := uint32(rng.Intn(40))
			rs.Stamps[pixel] = winner
			type frag struct {
				key, color, cc uint32
			}
			frags := make([]frag, n)
			for i := range frags {
				cc := uint32(3)
				if rng.Intn(4) == 0 {
					cc = 1
				}
				frags[i] = frag{key: uint32(rng.Intn(60)), color: 0x8000 | uint32(rng.Intn(1<<15)), cc: cc}
			}
			for _, f := range frags {
				appendFragment(rs, pixel, f.key, f.color, 0, f.cc)
			}
			// The expected result: the live fragments in key order,
			// one per key, the one nearest the list's head among
			// equals, which is the last appended, since an append
			// links the fragment at the head.
			live := make([]frag, 0, n)
			seen := map[uint32]bool{}
			for i := len(frags) - 1; i >= 0; i-- {
				f := frags[i]
				if f.key > winner && !seen[f.key] {
					seen[f.key] = true
					live = append(live, f)
				}
			}
			sort.Slice(live, func(a, b int) bool { return live[a].key < live[b].key })
			want := uint32(start)
			for _, f := range live {
				if out := ApplyColorCalc(f.cc, want, f.color, 0); out != NoStore {
					want = out
				}
			}
			Resolve(fb, rs, pixel)
			if got := uint32(fb.U16BE(pixel * 2)); got != want {
				t.Fatalf("%d fragments trial %d: pixel %04x, want %04x", n, trial, got, want)
			}
		}
	}
}
