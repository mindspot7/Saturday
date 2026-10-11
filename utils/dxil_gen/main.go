// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command dxil_gen compiles the compute packages to signed DXIL for
// the DX12 backend: it lowers the renderer's compute sources and the
// conformance sources, emits one HLSL source per entry point into
// the build tree, runs the DirectX Shader Compiler on each, and
// writes the blobs into the dxil directory of the dx12 package,
// which every Windows build embeds. The backend's own kernels, the
// fill, are written and compiled the same way into the backend set. The compiler must have
// dxil.dll beside it so the blobs are signed; an unsigned blob, or
// one without the root signature the shell declares, is rejected.
// With an empty compiler path the sources are written and nothing is
// compiled, for inspection on a machine without the compiler.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/compile/hlsl"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// placeholderBlob is the empty file committed in each output
// directory so the dx12 package's embed directives compile before
// the generator has run. It is never removed.
const placeholderBlob = "placeholder.dxil"

// set is one shader set: its directory name under the output
// directory and the compile stage's inputs.
type set struct {
	name   string
	parent compile.Source
	subs   []compile.Source
}

func main() {
	dxc := flag.String("dxc", "dxc", "the DirectX Shader Compiler executable, with dxil.dll beside it; empty writes the sources only")
	out := flag.String("out", "internal/hostgpu/dx12/dxil", "the blob output directory, the one the dx12 package embeds")
	src := flag.String("hlsl", "build/hlsl", "the HLSL source output directory")
	flag.Parse()
	log.SetFlags(0)
	parent, subs := core.ComputeSources()
	cparent, csub := conformance.AllSources()
	sets := []set{
		{name: "renderer", parent: parent, subs: subs},
		{name: "conformance", parent: cparent, subs: []compile.Source{csub}},
	}
	for _, s := range sets {
		if err := build(*dxc, filepath.Join(*src, s.name), filepath.Join(*out, s.name), s); err != nil {
			log.Fatal(err)
		}
	}
	backend := map[string]string{hlsl.FillEntry: hlsl.FillSource}
	if err := write(*dxc, filepath.Join(*src, "backend"), filepath.Join(*out, "backend"), backend); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("backend: %d kernels\n", len(backend))
}

// build lowers the set and writes it (write), one source and one blob
// per entry point.
func build(dxc, srcDir, blobDir string, s set) error {
	prog, err := compile.Load(s.parent, s.subs...)
	if err != nil {
		return err
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		return err
	}
	o, err := hlsl.Emit(mod)
	if err != nil {
		return err
	}
	sources := make(map[string]string, len(o.Entries))
	for _, e := range o.Entries {
		sources[e.Func] = e.Source
	}
	if err := write(dxc, srcDir, blobDir, sources); err != nil {
		return err
	}
	fmt.Printf("%s: %d entry points\n", s.name, len(o.Entries))
	return nil
}

// write removes both directories' earlier output, and writes each
// source by name into srcDir and compiles it into a blob of the same
// name in blobDir.
func write(dxc, srcDir, blobDir string, sources map[string]string) error {
	for _, pattern := range []string{filepath.Join(srcDir, "*.hlsl"), filepath.Join(blobDir, "*.dxil")} {
		old, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, f := range old {
			if filepath.Base(f) == placeholderBlob {
				continue
			}
			if err := os.Remove(f); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	if dxc != "" {
		if err := os.MkdirAll(blobDir, 0o755); err != nil {
			return err
		}
	}
	for name, source := range sources {
		src := filepath.Join(srcDir, name+".hlsl")
		if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
			return err
		}
		if dxc == "" {
			continue
		}
		blob := filepath.Join(blobDir, name+".dxil")
		cmd := exec.Command(dxc, "-T", "cs_6_0", "-E", "main", "-HV", "2021", "-O3", "-Gis",
			"-Qstrip_debug", "-Qstrip_reflect", "-Fo", blob, src)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := checkContainer(blob); err != nil {
			return err
		}
	}
	return nil
}

// checkContainer holds a compiled blob to the container shape the
// device needs: the magic, a digest that is not all zero, which an
// unsigned blob has, and a root signature part among the parts.
func checkContainer(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// The container header: the magic, a 16-byte digest, the version
	// as two 16-bit words, the total size, the part count, then the
	// part offsets. Each part starts with its four-character code and
	// its size.
	const header = 4 + 16 + 2 + 2 + 4 + 4
	if len(data) < header || string(data[:4]) != "DXBC" {
		return fmt.Errorf("%s: not a DXIL container", path)
	}
	signed := false
	for _, b := range data[4:20] {
		if b != 0 {
			signed = true
		}
	}
	if !signed {
		return fmt.Errorf("%s: the blob is not signed; dxil.dll must be beside the compiler", path)
	}
	if size := binary.LittleEndian.Uint32(data[24:]); int(size) != len(data) {
		return fmt.Errorf("%s: container size %d, file size %d", path, size, len(data))
	}
	parts := int(binary.LittleEndian.Uint32(data[28:]))
	if len(data) < header+4*parts {
		return fmt.Errorf("%s: part table past the end", path)
	}
	for i := 0; i < parts; i++ {
		off := int(binary.LittleEndian.Uint32(data[header+4*i:]))
		if off+8 > len(data) {
			return fmt.Errorf("%s: part %d past the end", path, i)
		}
		if string(data[off:off+4]) == "RTS0" {
			return nil
		}
	}
	return fmt.Errorf("%s: the blob carries no root signature", path)
}
