// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mobile is the Ebitengine mobile entry point for the Android
// frontend. It exposes a small gomobile-safe API for loading a disc and BIOS;
// Android's document picker can copy selected files to app cache and pass
// those paths to LoadDisc / LoadBIOS.
package mobile

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/mobile"
	"github.com/user-none/eblitui/romloader"
	"github.com/user-none/erings/core"
)

func init() {
	mobile.SetGame(&shell{})
}

const (
	buttonUp = iota
	buttonDown
	buttonLeft
	buttonRight
	buttonA
	buttonB
	buttonC
	buttonX
	buttonY
	buttonZ
	buttonL
	buttonR
	buttonStart
)

var session struct {
	sync.Mutex
	emu       *core.Emulator
	disc      *romloader.Disc
	bios      []byte
	buttons   uint32
	status    string
	started   bool
}

// Dummy is exported so ebitenmobile can generate Java bindings for this package.
func Dummy() {}

// LoadBIOS loads a user-provided Saturn BIOS from a path readable by the app.
// An empty path clears the configured BIOS. The path is normally an app-cache
// copy created from Android's Storage Access Framework URI.
func LoadBIOS(path string) string {
	session.Lock()
	defer session.Unlock()
	if strings.TrimSpace(path) == "" {
		session.bios = nil
		session.status = "BIOS cleared; HLE boot will be used"
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		session.status = "BIOS error: " + err.Error()
		return session.status
	}
	session.bios = data
	session.status = "BIOS loaded"
	return ""
}

// LoadDisc opens a .chd or .cue disc image and starts the Saturn core.
// For CUE images, the referenced BIN files must remain beside the CUE file.
// The caller should copy the CUE and all referenced track files into one
// app-cache directory before calling this function.
func LoadDisc(path string) string {
	session.Lock()
	defer session.Unlock()

	if strings.TrimSpace(path) == "" {
		return "Select a .CHD or .CUE disc image."
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".chd" && ext != ".cue" {
		return "Unsupported disc format. Choose a .CHD or .CUE file."
	}

	disc, err := romloader.OpenDisc(path)
	if err != nil {
		session.status = "Disc error: " + err.Error()
		return session.status
	}

	if session.started && session.emu != nil {
		session.emu.Close()
		session.started = false
	}
	if session.disc != nil {
		_ = session.disc.Close()
		session.disc = nil
	}

	emu := core.NewEmulator()
	if len(session.bios) != 0 {
		if err := emu.SetBIOS("main_bios", session.bios); err != nil {
			_ = disc.Close()
			session.status = "BIOS error: " + err.Error()
			return session.status
		}
	}
	emu.SetDisc(disc)
	if err := emu.Start(); err != nil {
		_ = disc.Close()
		session.status = "Boot error: " + err.Error()
		return session.status
	}

	session.emu = emu
	session.disc = disc
	session.started = true
	session.status = "Running: " + filepath.Base(path)
	return ""
}

// SetButton updates one physical Saturn control. Button IDs are: 0 up,
// 1 down, 2 left, 3 right, 4 A, 5 B, 6 C, 7 X, 8 Y, 9 Z, 10 L, 11 R, 12 Start.
func SetButton(button int, pressed bool) {
	if button < buttonUp || button > buttonStart {
		return
	}
	session.Lock()
	defer session.Unlock()
	if pressed {
		session.buttons |= 1 << uint(button)
	} else {
		session.buttons &^= 1 << uint(button)
	}
}

// GetStatus returns the most recent user-facing load or runtime status.
func GetStatus() string {
	session.Lock()
	defer session.Unlock()
	if session.status == "" {
		return "Ready. Select a Saturn disc to begin."
	}
	return session.status
}

type shell struct{}

func (g *shell) Update() error {
	session.Lock()
	defer session.Unlock()
	if session.emu != nil && session.started {
		session.emu.SetInput(0, session.buttons)
		session.emu.RunFrame()
	}
	return nil
}

func (g *shell) Draw(screen *ebiten.Image) {
	session.Lock()
	defer session.Unlock()

	screen.Fill(color.RGBA{R: 8, G: 12, B: 20, A: 255})
	if session.emu != nil && session.started {
		pixels := session.emu.GetFramebuffer()
		stride := session.emu.GetFramebufferStride()
		height := session.emu.GetActiveHeight()
		width := stride / 4
		if width > 0 && height > 0 && len(pixels) >= stride*height {
			// WritePixels expects tightly packed RGBA. Saturn framebuffer
			// rows are tightly packed today; retain a safe row-copy path in
			// case the renderer's stride differs from width*4.
			if stride == width*4 {
				screen.WritePixels(pixels[:stride*height])
			} else {
				tight := make([]byte, width*height*4)
				for y := 0; y < height; y++ {
					copy(tight[y*width*4:(y+1)*width*4], pixels[y*stride:y*stride+width*4])
				}
				screen.WritePixels(tight)
			}
			return
		}
	}
	status := session.status
	if status == "" {
		status = "Ready. Select a Saturn disc to begin."
	}
	ebitenutil.DebugPrint(screen, fmt.Sprintf("SATURDAY\n\nSega Saturn emulator\n\n%s\n\nSupports CHD and CUE/BIN", status))
}

func (g *shell) Layout(outsideWidth, outsideHeight int) (int, int) {
	if outsideWidth < 1 || outsideHeight < 1 {
		return 704, 512
	}
	return outsideWidth, outsideHeight
}
