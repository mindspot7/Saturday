// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mobile is the Ebitengine mobile entry point for the Android
// frontend. The initial screen is intentionally a shell: disc/BIOS loading
// and the emulator render loop will be connected in the next milestone.
package mobile

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/mobile"
)

func init() {
	mobile.SetGame(&shell{})
}

// Dummy is exported so ebitenmobile can generate Java bindings for this package.
func Dummy() {}

type shell struct{}

func (g *shell) Update() error { return nil }

func (g *shell) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 8, G: 12, B: 20, A: 255})
	ebitenutil.DebugPrint(screen, "SATURDAY\n\nSega Saturn emulator\nAndroid frontend scaffold\n\nNext: disc / BIOS selection\nand core rendering")
}

func (g *shell) Layout(outsideWidth, outsideHeight int) (int, int) {
	if outsideWidth < 1 || outsideHeight < 1 {
		return 704, 512
	}
	return outsideWidth, outsideHeight
}
