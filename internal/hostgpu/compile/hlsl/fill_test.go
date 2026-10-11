// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hlsl

import (
	"fmt"
	"strings"
	"testing"
)

// TestFillSource holds the fill kernel's source to the layout the
// device binds it with: the root signature, the workgroup, and the
// constants in the order of their word positions.
func TestFillSource(t *testing.T) {
	l := FillLayout()
	for _, want := range []string{
		fmt.Sprintf("[RootSignature(%q)]", l.RootSignature()),
		fmt.Sprintf("[numthreads(%d, %d, 1)]", l.Workgroup[0], l.Workgroup[1]),
		"uint f_count; uint f_value; uint f_base;",
	} {
		if !strings.Contains(FillSource, want) {
			t.Errorf("fill source lacks %s", want)
		}
	}
	if FillCountWord != 0 || FillValueWord != 1 || FillBaseWord != 2 || FillConstants != 3 {
		t.Error("fill constant positions do not follow the source's declaration order")
	}
	if err := l.check(); err != nil {
		t.Error(err)
	}
}
