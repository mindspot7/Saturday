// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dx12

import (
	"embed"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/conformance"
	"github.com/user-none/erings/internal/hostgpu/devicetest"
)

// conformanceBlobs is the conformance set the generator wrote.
//
//go:embed dxil/conformance/*.dxil
var conformanceBlobs embed.FS

// conformanceShaders is the conformance set by entry name, skipping
// the test when the directory holds only the placeholder.
func conformanceShaders(tb testing.TB) fs.FS {
	tb.Helper()
	sub, err := fs.Sub(conformanceBlobs, "dxil/conformance")
	if err != nil {
		tb.Fatal(err)
	}
	if !hasBlobs(sub) {
		tb.Skip("no conformance shaders: run make dxil")
	}
	return sub
}

// openDevice opens the first hardware adapter with the conformance
// shaders, skipping the test where there is none, and closes it when
// the test ends.
func openDevice(tb testing.TB) *device {
	tb.Helper()
	dev := openWithShaders(tb, conformanceShaders(tb))
	tb.Logf("device %s", dev.Name())
	return dev
}

// TestRoundTrip runs devicetest.RoundTrip on the device.
func TestRoundTrip(t *testing.T) {
	devicetest.RoundTrip(t, openDevice(t))
}

// TestSubmissionsReleased runs devicetest.SubmissionsReleased on the
// device.
func TestSubmissionsReleased(t *testing.T) {
	devicetest.SubmissionsReleased(t, openDevice(t))
}

// TestCloseWhileEncoding runs devicetest.CloseWhileEncoding on the
// device, which openDevice's cleanup closes.
func TestCloseWhileEncoding(t *testing.T) {
	devicetest.CloseWhileEncoding(t, openDevice(t))
}

// TestEncodeAfterCommit runs devicetest.EncodeAfterCommit on the
// device.
func TestEncodeAfterCommit(t *testing.T) {
	devicetest.EncodeAfterCommit(t, openDevice(t))
}

// TestHostErrorReported runs devicetest.HostErrorReported on the
// device.
func TestHostErrorReported(t *testing.T) {
	devicetest.HostErrorReported(t, openDevice(t))
}

// TestCommandsNoAllocs runs devicetest.CommandsNoAllocs on the
// device.
func TestCommandsNoAllocs(t *testing.T) {
	devicetest.CommandsNoAllocs(t, openDevice(t))
}

// TestMissingBlob checks that a shader set without an entry's blob
// fails Build with the entry's name and ErrUnavailable, the reason
// the renderer logs when a build embeds no shaders.
func TestMissingBlob(t *testing.T) {
	dev := openWithShaders(t, fstest.MapFS{})
	parent, sub := conformance.Sources()
	if _, err := hostgpu.Build(dev, parent, sub); err == nil || !errors.Is(err, hostgpu.ErrUnavailable) {
		t.Fatalf("build without blobs: %v", err)
	}
	openWithShaders(t, missing{})
}

// openWithShaders opens the first hardware adapter with a shader set
// of the test's own, skipping the test where there is none, and
// closes it when the test ends.
func openWithShaders(tb testing.TB, shaders fs.FS) *device {
	tb.Helper()
	dev, err := Open(shaders)
	if err != nil {
		if errors.Is(err, hostgpu.ErrUnavailable) {
			tb.Skip(err)
		}
		tb.Fatal(err)
	}
	tb.Cleanup(dev.Close)
	return dev.(*device)
}

// TestBadBlob checks that a truncated blob is refused with the code
// rather than a crash.
func TestBadBlob(t *testing.T) {
	blob, err := fs.ReadFile(conformanceShaders(t), "Words.dxil")
	if err != nil {
		t.Fatal(err)
	}
	set := fstest.MapFS{"Words.dxil": {Data: blob[:len(blob)/2]}}
	for _, e := range conformance.Entries {
		if e.Name != "Words" {
			set[e.Name+".dxil"] = &fstest.MapFile{Data: blob}
		}
	}
	dev := openWithShaders(t, set)
	parent, sub := conformance.Sources()
	if _, err := hostgpu.Build(dev, parent, sub); err == nil {
		t.Fatal("a truncated blob built")
	}
}
