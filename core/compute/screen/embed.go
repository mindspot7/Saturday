// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "embed"

// Source is this package's Go source for the GPU renderer's compile
// stage.
//
// The files are named one by one: the compile stage takes everything
// Source holds, so this file and the package's tests must stay out of
// it. TestEmbeddedSource holds the list against the directory.
//
//go:embed cell.go color.go composite.go gradation.go nbg.go pages.go
//go:embed palette.go rbg.go screen.go sprite.go window.go
var Source embed.FS

// Entries names the package's entry points: the span entries a row's
// chain names, then the frame-level ones dispatched between rows. The
// order is this list's own. A renderer indexes them by name.
var Entries = [...]string{
	"NBGCellSpan", "NBGBitmapSpan", "RBGCellSpan", "RBGBitmapSpan", "GradationSpan", "CompositeSpan",
	"BuildPalette", "CopyVRAMPages", "CopyCRAMPages",
}
