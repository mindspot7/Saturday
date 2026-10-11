// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import (
	"errors"
	"slices"
	"testing"
)

// TestLedgerCommitRelease checks the ledger's numbering and release:
// numbers rise by one from 1, Next names the number the next Commit
// returns, a wait on a later number reports every earlier submission
// with it in commit order and the last one's number, a release drops
// exactly those, a wait on a released number reports nothing, and a
// number no Commit returned is ErrNotCommitted.
func TestLedgerCommitRelease(t *testing.T) {
	var l Ledger[string]
	if l.Newest() != 0 {
		t.Fatalf("empty ledger's newest %d, want 0", l.Newest())
	}
	if vals, _, err := l.Pending(0); err != nil || len(vals) != 0 {
		t.Fatalf("pending on zero: %v %v", vals, err)
	}
	if _, _, err := l.Pending(1); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("pending before any commit: %v", err)
	}
	if n := l.Next(); n != 1 {
		t.Fatalf("next %d, want 1", n)
	}
	a, b, c := l.Commit("a"), l.Commit("b"), l.Commit("c")
	if a != 1 || b != 2 || c != 3 || l.Newest() != 3 || l.Next() != 4 {
		t.Fatalf("numbers %d %d %d newest %d next %d", a, b, c, l.Newest(), l.Next())
	}
	vals, last, err := l.Pending(b)
	if err != nil || last != b || !slices.Equal(vals, []string{"a", "b"}) {
		t.Fatalf("pending on %d: %v %d %v", b, vals, last, err)
	}
	l.Release(len(vals))
	if vals, _, err := l.Pending(b); err != nil || len(vals) != 0 {
		t.Fatalf("pending on a released number: %v %v", vals, err)
	}
	if vals, last, err := l.Pending(c); err != nil || last != c || !slices.Equal(vals, []string{"c"}) {
		t.Fatalf("pending on %d: %v %d %v", c, vals, last, err)
	}
	if _, _, err := l.Pending(c + 1); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("pending past the last commit: %v", err)
	}
	l.Release(1)
	if l.Newest() != 0 {
		t.Fatalf("newest %d after releasing every submission, want 0", l.Newest())
	}
	if d := l.Commit("d"); d != 4 {
		t.Fatalf("commit after release %d, want 4", d)
	}
}

// TestLedgerPendingAliases checks that Pending's values are the
// ledger's own storage, so a backend may take an entry's address for
// a driver call and the call sees the committed value.
func TestLedgerPendingAliases(t *testing.T) {
	var l Ledger[[2]int]
	l.Commit([2]int{1, 2})
	vals, _, _ := l.Pending(1)
	vals[0][1] = 9
	again, _, _ := l.Pending(1)
	if again[0][1] != 9 {
		t.Fatal("pending values are a copy")
	}
}

// TestLedgerNoAllocs checks that a steady commit, pending, and release
// cycle allocates nothing once the storage has grown, which the
// warm-up run of AllocsPerRun does. One measured run, since
// AllocsPerRun divides the count by the runs and a run of several
// would hide an allocation made every other cycle.
func TestLedgerNoAllocs(t *testing.T) {
	var l Ledger[int]
	cycle := func() {
		s := l.Commit(1)
		l.Commit(2)
		vals, _, _ := l.Pending(s + 1)
		l.Release(len(vals))
	}
	if allocs := testing.AllocsPerRun(1, cycle); allocs != 0 {
		t.Errorf("%.0f allocations per cycle", allocs)
	}
}
