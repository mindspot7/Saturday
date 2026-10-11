// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package hostgpu

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"unsafe"

	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// TestDispatchLayout pins the dispatch record to the 20-byte block
// the backends' emitted dispatch struct declares: five words in
// order.
func TestDispatchLayout(t *testing.T) {
	var d Dispatch
	if unsafe.Sizeof(d) != 20 {
		t.Fatalf("dispatch record is %d bytes, want 20", unsafe.Sizeof(d))
	}
	offsets := []uintptr{
		unsafe.Offsetof(d.BoundX), unsafe.Offsetof(d.BoundY), unsafe.Offsetof(d.ParamsOffset),
		unsafe.Offsetof(d.ParamsStride), unsafe.Offsetof(d.Segment),
	}
	for i, off := range offsets {
		if off != uintptr(i)*4 {
			t.Errorf("field %d at byte %d, want %d", i, off, i*4)
		}
	}
}

// countingDevice is a device whose Build returns one pipeline per
// entry, or fewer when short is set.
type countingDevice struct {
	short  bool
	builds int
}

func (d *countingDevice) Name() string               { return "counting" }
func (d *countingDevice) GroupThreads(GroupLoad) int { return 1 }

func (d *countingDevice) Build(mod *compile.Module) ([]Pipeline, error) {
	d.builds++
	n := len(mod.Entries)
	if d.short {
		n--
	}
	pipes := make([]Pipeline, n)
	for i := range pipes {
		pipes[i] = i
	}
	return pipes, nil
}

func (d *countingDevice) NewBuffer(int, BufferKind) (Buffer, error) {
	return nil, errors.New("no buffers")
}
func (d *countingDevice) Begin() (Commands, error) { return nil, errors.New("no commands") }
func (d *countingDevice) Wait(Submission) error    { return nil }
func (d *countingDevice) Close()                   {}

// TestBuildResolvesByName builds the conformance sources on a device
// and checks every entry point resolves to the pipeline the device
// built for it, in the module's order, and an unknown name fails.
func TestBuildResolvesByName(t *testing.T) {
	parent, sub := conformance.Sources()
	dev := &countingDevice{}
	prog, err := Build(dev, parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Device != dev {
		t.Fatal("program does not hold the device it was built on")
	}
	for i, e := range conformance.Entries {
		pipe, err := prog.Pipeline(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		if pipe != i {
			t.Errorf("%s resolved to pipeline %v, want %d", e.Name, pipe, i)
		}
	}
	if _, err := prog.Pipeline("NoSuchEntry"); err == nil {
		t.Error("unknown entry point resolved")
	}
}

// TestBuildErrors checks a source outside the subset, a device that
// builds fewer pipelines than entries, and the same source given
// twice, which repeats every entry name, each fail the build. A
// repeated name fails before the device builds anything.
func TestBuildErrors(t *testing.T) {
	parent, sub := conformance.Sources()
	if _, err := Build(&countingDevice{short: true}, parent, sub); err == nil || !strings.Contains(err.Error(), "pipelines") {
		t.Errorf("short build: %v", err)
	}
	bad := Source{Path: sub.Path, Files: fstest.MapFS{"bad.go": {Data: []byte("package conformance\n\nimport \"fmt\"\n\nfunc Bad(i uint32) { fmt.Println(i) }\n")}}, Entries: []string{"Bad"}}
	if _, err := Build(&countingDevice{}, parent, bad); err == nil {
		t.Error("a source outside the subset built")
	}
	dup := &countingDevice{}
	if _, err := Build(dup, parent, sub, sub); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate entry name: %v", err)
	}
	if dup.builds != 0 {
		t.Errorf("duplicate entry name: the device built %d times before the error", dup.builds)
	}
}
