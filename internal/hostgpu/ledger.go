// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import "errors"

// ErrNotCommitted is the error of a wait on a sequence number no
// Commit has returned.
var ErrNotCommitted = errors.New("wait on a submission that was not committed")

// Ledger is a device's committed submissions in the order committed,
// each with the backend's value for it: the sequence numbers a
// backend hands out and the in-flight list its Wait releases. A
// device's queue completes submissions in order, so a wait on one
// releases every submission committed before it. The zero value is
// empty, and its first Commit returns 1. The ledger keeps its storage
// across commits and releases, so a steady cycle of commit, wait,
// and release allocates nothing once the list has grown.
//
// The submissions in flight are numbered consecutively up to last, so
// vals[i] is submission last-len(vals)+1+i.
type Ledger[T any] struct {
	last Submission
	vals []T
}

// Next is the sequence number the next Commit returns, for a backend
// that hands it to the driver before it commits, as a fence value.
func (l *Ledger[T]) Next() Submission {
	return l.last + 1
}

// Commit appends v under the next sequence number and returns it.
func (l *Ledger[T]) Commit(v T) Submission {
	l.last++
	l.vals = append(l.vals, v)
	return l.last
}

// Pending reports the leading submissions at or below s: their values
// in commit order and the sequence number of the last of them. The
// values are the ledger's own storage, valid until the next Commit or
// Release, so a backend may take the address of one for a driver call.
// No submission pending is an empty slice. A number no Commit returned
// is ErrNotCommitted.
func (l *Ledger[T]) Pending(s Submission) ([]T, Submission, error) {
	if s > l.last {
		return nil, 0, ErrNotCommitted
	}
	first := l.last - Submission(len(l.vals)) + 1
	if len(l.vals) == 0 || s < first {
		return nil, 0, nil
	}
	return l.vals[:s-first+1], s, nil
}

// Release removes the first n submissions, the ones Pending reported
// and the backend has waited on.
func (l *Ledger[T]) Release(n int) {
	rest := copy(l.vals, l.vals[n:])
	clear(l.vals[rest:])
	l.vals = l.vals[:rest]
}

// Newest is the sequence number of the last submission in flight, or
// zero when none is.
func (l *Ledger[T]) Newest() Submission {
	if len(l.vals) == 0 {
		return 0
	}
	return l.last
}
