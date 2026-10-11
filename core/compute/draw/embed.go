// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "embed"

// Source is this package's Go source for the GPU renderer's compile
// stage.
//
// The files are named one by one: the compile stage takes everything
// Source holds, so this file and the package's tests must stay out of
// it. TestEmbeddedSource holds the list against the directory.
//
//go:embed clip.go dda.go draw.go drawop.go erase.go generate.go
//go:embed gouraud.go pixel.go quad.go records.go segment.go setup.go
//go:embed sprite.go square.go texel.go
var Source embed.FS

// Entries names the package's entry points: the op draw's two
// passes, the order stamp clear, the fragment list clear, the
// fragment resolve, the VDP1 VRAM page copy, and the frame buffer
// write copy. A renderer indexes them by name.
var Entries = [...]string{"GenerateTag", "GenerateWrite", "ClearStamps", "ClearLists", "Resolve", "CopyVDP1Pages", "CopyFBPages"}
