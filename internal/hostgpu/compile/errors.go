// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"fmt"
	"go/token"
	"sort"
	"strings"
)

// Error is one compile-stage diagnostic at a source position.
type Error struct {
	Pos token.Position
	Msg string
}

func (e Error) Error() string {
	if e.Pos.IsValid() {
		return e.Pos.String() + ": " + e.Msg
	}
	return e.Msg
}

// ErrorList collects every diagnostic of a run so a source with several
// violations reports all of them at once.
type ErrorList []Error

func (l ErrorList) Error() string {
	if len(l) == 0 {
		return "no diagnostics"
	}
	var b strings.Builder
	for i, e := range l {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	if len(l) > 1 {
		fmt.Fprintf(&b, "\n(%d diagnostics)", len(l))
	}
	return b.String()
}

// diagnostics accumulates the diagnostics of a run against a file set.
type diagnostics struct {
	fset *token.FileSet
	list ErrorList
}

func (e *diagnostics) add(pos token.Pos, format string, args ...any) {
	e.list = append(e.list, Error{Pos: e.fset.Position(pos), Msg: fmt.Sprintf(format, args...)})
}

// err returns the diagnostics sorted by position, or nil.
func (e *diagnostics) err() error {
	if len(e.list) == 0 {
		return nil
	}
	sort.SliceStable(e.list, func(i, j int) bool {
		a, b := e.list[i].Pos, e.list[j].Pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return e.list
}
