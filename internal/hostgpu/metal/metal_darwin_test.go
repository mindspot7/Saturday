// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package metal

import (
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/devicetest"
)

// openDevice opens the device for a test, skipping when Metal is
// unavailable, and closes it when the test ends.
func openDevice(t testing.TB) *device {
	t.Helper()
	dev, err := Open()
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(dev.Close)
	t.Logf("device %s", dev.Name())
	return dev.(*device)
}

// roundTripPipeline compiles a benchmark kernel source into a
// pipeline with the workgroup given. Every benchmark kernel binds
// two buffers and its dispatch record after them, at index 2.
func roundTripPipeline(t testing.TB, dev *device, source string, workgroup [2]int) *pipeline {
	t.Helper()
	pool := pushPool()
	defer popPool(&dev.args, pool)
	lib, err := dev.compile(source)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Send(selRelease)
	pipe, err := dev.pipeline(lib, "k0", workgroup, 2)
	if err != nil {
		t.Fatal(err)
	}
	return pipe
}

// TestFitWorkgroup holds the threadgroup a pipeline's limit gives each
// of the emitter's workgroups: the workgroup when it fits, the largest
// power of two at or below the limit when not, a span keeping its
// width while that fits.
func TestFitWorkgroup(t *testing.T) {
	for _, c := range []struct {
		workgroup [2]int
		limit     int
		want      [2]uintptr
	}{
		{[2]int{128, 1}, 1024, [2]uintptr{128, 1}},
		{[2]int{128, 1}, 128, [2]uintptr{128, 1}},
		{[2]int{128, 1}, 96, [2]uintptr{64, 1}},
		{[2]int{64, 1}, 40, [2]uintptr{32, 1}},
		{[2]int{8, 8}, 64, [2]uintptr{8, 8}},
		{[2]int{8, 8}, 48, [2]uintptr{8, 4}},
		{[2]int{8, 8}, 8, [2]uintptr{8, 1}},
		{[2]int{8, 8}, 6, [2]uintptr{4, 1}},
		{[2]int{64, 1}, 1, [2]uintptr{1, 1}},
	} {
		got := fitWorkgroup(c.workgroup, c.limit)
		if got.width != c.want[0] || got.height != c.want[1] || got.depth != 1 {
			t.Errorf("workgroup %v at limit %d: %d x %d x %d, want %v", c.workgroup, c.limit, got.width, got.height, got.depth, c.want)
		}
	}
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

// TestCompileError checks that a compiler failure comes back as an
// error with the compiler's message rather than a crash.
func TestCompileError(t *testing.T) {
	dev := openDevice(t)
	pool := pushPool()
	defer popPool(&dev.args, pool)
	if _, err := dev.compile("kernel void k0(uint gid [[thread_position_in_grid]]) { undefined_symbol(); }"); err == nil {
		t.Fatal("compile of invalid source succeeded")
	}
}

// roundTripKernel writes one word per thread over a composite-scale
// grid, the transport shape of a frame.
const roundTripKernel = `#include <metal_stdlib>
using namespace metal;
struct Dispatch { uint bx; uint by; uint off; uint stride; uint seg; };
kernel void k0(device const uchar* b0 [[buffer(0)]], device uint* b1 [[buffer(1)]], constant Dispatch& d [[buffer(2)]], uint2 gid [[thread_position_in_grid]]) {
	if (gid.x >= d.bx || gid.y >= d.by) return;
	b1[gid.y * d.bx + gid.x] = (uint)b0[(gid.y * d.bx + gid.x) & 0x7FFFF] * 3u;
}
`

// BenchmarkSubmitWait measures an empty command buffer's commit and
// wait: the fence cost with no work.
func BenchmarkSubmitWait(b *testing.B) {
	dev := openDevice(b)
	for i := 0; i < b.N; i++ {
		cmds, err := dev.Begin()
		if err != nil {
			b.Fatal(err)
		}
		if err := dev.Wait(cmds.Commit()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRoundTrip measures a frame-shaped round trip: a 580 KB
// upload into a shared buffer, one dispatch over a 704x512 grid
// writing 1.4 MB, the wait, and a read of the result. Both buffers
// are upload buffers, which this backend places in shared storage, so
// the kernel writes the memory the CPU reads: the benchmark measures
// that placement and holds to this backend alone, not to the device
// contract, under which a kernel does not write an upload buffer.
func BenchmarkRoundTrip(b *testing.B) {
	dev := openDevice(b)
	pipe := roundTripPipeline(b, dev, roundTripKernel, [2]int{8, 8})
	const w, h = 704, 512
	src := make([]byte, 580*1024)
	for i := range src {
		src[i] = byte(i)
	}
	inBuf, err := dev.NewBuffer(len(src), hostgpu.BufferUpload)
	if err != nil {
		b.Fatal(err)
	}
	outBuf, err := dev.NewBuffer(w*h*4, hostgpu.BufferUpload)
	if err != nil {
		b.Fatal(err)
	}
	defer inBuf.Release()
	defer outBuf.Release()
	buffers := []hostgpu.Buffer{inBuf, outBuf}
	var sink uint32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(inBuf.Bytes(), src)
		cmds, err := dev.Begin()
		if err != nil {
			b.Fatal(err)
		}
		cmds.Dispatch(pipe, buffers, hostgpu.Dispatch{BoundX: w, BoundY: h}, [2]int{w, h})
		if err := dev.Wait(cmds.Commit()); err != nil {
			b.Fatal(err)
		}
		out := outBuf.Bytes()
		for j := 0; j < len(out); j += 4096 {
			sink += uint32(out[j])
		}
	}
	_ = sink
}

// The VDP1 key kernels: one thread per dot, the dot's pixel index
// from the thread's grid index masked by the dispatch record's seg
// field, and the thread's grid index as its draw order key. The
// store kernel writes the key to the pixel's word, the atomic kernel
// raises the word to the key, and the write kernel stores the dot
// whose key the word holds, the second pass of a winner draw.
const (
	keyStoreKernel = `#include <metal_stdlib>
using namespace metal;
struct Dispatch { uint bx; uint by; uint off; uint stride; uint seg; };
kernel void k0(device uint* keys [[buffer(0)]], device uint* out [[buffer(1)]], constant Dispatch& d [[buffer(2)]], uint gid [[thread_position_in_grid]]) {
	if (gid >= d.bx) return;
	keys[gid & d.seg] = gid;
}
`
	keyAtomicMaxKernel = `#include <metal_stdlib>
using namespace metal;
struct Dispatch { uint bx; uint by; uint off; uint stride; uint seg; };
kernel void k0(device atomic_uint* keys [[buffer(0)]], device uint* out [[buffer(1)]], constant Dispatch& d [[buffer(2)]], uint gid [[thread_position_in_grid]]) {
	if (gid >= d.bx) return;
	atomic_fetch_max_explicit(&keys[gid & d.seg], gid, memory_order_relaxed);
}
`
	keyWinnerWriteKernel = `#include <metal_stdlib>
using namespace metal;
struct Dispatch { uint bx; uint by; uint off; uint stride; uint seg; };
kernel void k0(device const uint* keys [[buffer(0)]], device uint* out [[buffer(1)]], constant Dispatch& d [[buffer(2)]], uint gid [[thread_position_in_grid]]) {
	if (gid >= d.bx) return;
	uint i = gid & d.seg;
	if (keys[i] == gid) out[i] = gid * 3u;
}
`
)

// keyWords is the key buffer: one word per pixel of a 256 KB frame
// buffer at 16 bits per pixel.
const keyWords = 1 << 17

// BenchmarkVDP1AtomicMax measures the device cost of deciding a
// pixel's last dot by an atomic max on a per-pixel key word, against
// a plain store of the same word, per dot, over dot counts and
// overdraw patterns from the VDP1's bounds. Each pattern is a dot
// count and a pixel mask: distinct touches every pixel once, frame
// is the frame's dot bound spread over every pixel, hot is the same
// count over 4096 pixels, and apex is every dot on one pixel, the
// converging quad. The store form is the floor, max is the tag pass
// alone, and max+write is the tag pass followed by the winner write
// pass behind a barrier, the whole replace path. The key buffer is
// cleared outside the timed region so every atomic raises its word.
// Reported per dot in ns/dot. The buffers are upload buffers in this
// backend's shared storage, which the kernels write and the CPU
// clears, as BenchmarkRoundTrip's are.
func BenchmarkVDP1AtomicMax(b *testing.B) {
	dev := openDevice(b)
	store := roundTripPipeline(b, dev, keyStoreKernel, [2]int{128, 1})
	amax := roundTripPipeline(b, dev, keyAtomicMaxKernel, [2]int{128, 1})
	write := roundTripPipeline(b, dev, keyWinnerWriteKernel, [2]int{128, 1})
	keys, err := dev.NewBuffer(keyWords*4, hostgpu.BufferUpload)
	if err != nil {
		b.Fatal(err)
	}
	out, err := dev.NewBuffer(keyWords*4, hostgpu.BufferUpload)
	if err != nil {
		b.Fatal(err)
	}
	defer keys.Release()
	defer out.Release()
	buffers := []hostgpu.Buffer{keys, out}
	patterns := []struct {
		name string
		dots uint32
		mask uint32
	}{
		{"distinct", keyWords, keyWords - 1},
		{"frame", 1 << 20, keyWords - 1},
		{"hot", 1 << 20, 1<<12 - 1},
		{"apex", 1 << 20, 0},
	}
	forms := []struct {
		name  string
		pipes []*pipeline
	}{
		{"store", []*pipeline{store}},
		{"max", []*pipeline{amax}},
		{"max+write", []*pipeline{amax, write}},
	}
	for _, p := range patterns {
		for _, f := range forms {
			b.Run(p.name+"/"+f.name, func(b *testing.B) {
				rec := hostgpu.Dispatch{BoundX: p.dots, BoundY: 1, Segment: p.mask}
				grid := [2]int{int(p.dots), 1}
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					clear(keys.Bytes())
					b.StartTimer()
					cmds, err := dev.Begin()
					if err != nil {
						b.Fatal(err)
					}
					for j, pipe := range f.pipes {
						if j > 0 {
							cmds.Barrier()
						}
						cmds.Dispatch(pipe, buffers, rec, grid)
					}
					if err := dev.Wait(cmds.Commit()); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(p.dots), "ns/dot")
			})
		}
	}
}
