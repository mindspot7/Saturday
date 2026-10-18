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
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
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

var (
	audioContextOnce sync.Once
	audioContext     *audio.Context
	audioContextErr  error
)

var session struct {
	sync.Mutex
	emu         *core.Emulator
	disc        *romloader.Disc
	bios        []byte
	backupPath  string
	backupRAM   []byte
	lastBackupFrame uint64
	buttons     uint32
	status      string
	started     bool
	frameCount  uint64
	audioStream *pcmRing
	audioPlayer *audio.Player
}

// pcmRing is a bounded, non-blocking producer/consumer queue for interleaved
// 44.1 kHz stereo signed-16-bit PCM. If emulation runs ahead, old audio is
// dropped rather than blocking the video/game loop. If audio runs ahead, Read
// fills the remainder with silence to keep the audio device clock moving.
type pcmRing struct {
	mu    sync.Mutex
	data  []byte
	read  int
	write int
	size  int
}

func newPCMRing() *pcmRing {
	// Two seconds of stereo 16-bit PCM.
	return &pcmRing{data: make([]byte, 44100*4*2)}
}

func (r *pcmRing) Push(samples []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Samples are interleaved stereo: keep writes and overflow drops on
	// complete 4-byte frames so a full ring never shifts channel alignment.
	for i := 0; i < len(samples); i += 2 {
		if r.size+4 > len(r.data) {
			// Audio reads are frame-aligned, so the oldest complete stereo
			// frame can be discarded without swapping left/right channels.
			r.read = (r.read + 4) % len(r.data)
			r.size -= 4
		}
		left := uint16(samples[i])
		r.data[r.write] = byte(left)
		r.write = (r.write + 1) % len(r.data)
		r.data[r.write] = byte(left >> 8)
		r.write = (r.write + 1) % len(r.data)

		var right uint16
		if i+1 < len(samples) {
			right = uint16(samples[i+1])
		}
		r.data[r.write] = byte(right)
		r.write = (r.write + 1) % len(r.data)
		r.data[r.write] = byte(right >> 8)
		r.write = (r.write + 1) % len(r.data)
		r.size += 4
	}
}

func (r *pcmRing) Read(dst []byte) (int, error) {
	clear(dst)
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(dst)
	if n > r.size {
		n = r.size
	}
	first := n
	if first > len(r.data)-r.read {
		first = len(r.data) - r.read
	}
	copy(dst[:first], r.data[r.read:r.read+first])
	if first < n {
		copy(dst[first:n], r.data[:n-first])
	}
	r.read = (r.read + n) % len(r.data)
	r.size -= n
	return len(dst), nil
}

func ensureAudioLocked() {
	if session.audioPlayer != nil {
		return
	}
	audioContextOnce.Do(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				audioContextErr = fmt.Errorf("audio initialization panic: %v", recovered)
			}
		}()
		audioContext = audio.NewContext(44100)
	})
	if audioContextErr != nil || audioContext == nil {
		if audioContextErr != nil {
			session.status = "Audio unavailable: " + audioContextErr.Error()
		}
		return
	}
	session.audioStream = newPCMRing()
	player, err := audioContext.NewPlayer(session.audioStream)
	if err != nil {
		session.audioStream = nil
		session.status = "Audio unavailable: " + err.Error()
		return
	}
	player.SetBufferSize(100 * time.Millisecond)
	player.Play()
	session.audioPlayer = player
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
// SetBackupRAMPath configures the app-private Saturn backup-RAM file and
// restores it for the next emulator instance. Invalid or missing files are
// ignored, leaving the core's freshly formatted 32 KB backup RAM in place.
func SetBackupRAMPath(path string) {
	session.Lock()
	defer session.Unlock()
	session.backupPath = strings.TrimSpace(path)
	session.backupRAM = nil
	if session.backupPath == "" {
		return
	}
	data, err := os.ReadFile(session.backupPath)
	if err == nil && len(data) == 32*1024 {
		session.backupRAM = data
	}
}

// SaveBackupRAM flushes the current internal backup RAM to the configured
// app-private file. It is safe to call when no game is running.
func SaveBackupRAM() {
	session.Lock()
	defer session.Unlock()
	persistBackupRAMLocked()
}

func persistBackupRAMLocked() {
	if session.backupPath == "" || session.emu == nil || !session.started {
		return
	}
	data := session.emu.GetSRAM()
	if len(data) != 32*1024 {
		return
	}
	tmp := session.backupPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	if err := os.Rename(tmp, session.backupPath); err != nil {
		_ = os.Remove(tmp)
		return
	}
	session.backupRAM = data
	session.lastBackupFrame = session.frameCount
}

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
		persistBackupRAMLocked()
		session.emu.Close()
		session.started = false
	}
	if session.disc != nil {
		_ = session.disc.Close()
		session.disc = nil
	}

	emu := core.NewEmulator()
	if len(session.backupRAM) == 32*1024 {
		emu.SetSRAM(session.backupRAM)
	}
	if len(session.bios) != 0 {
		if err := emu.SetBIOS("main_bios", session.bios); err != nil {
			_ = disc.Close()
			session.status = "BIOS error: " + err.Error()
			return session.status
		}
	}
	emu.SetDisc(disc)
	emu.SetOption("upscale", configuredUpscale)
	emu.SetOption("fast_boot", fmt.Sprintf("%t", configuredFastBoot))
	if err := emu.SetRendererByName(configuredRenderer); err != nil {
		session.status = "Renderer fallback: " + err.Error()
	}
	if err := emu.Start(); err != nil {
		_ = disc.Close()
		session.status = "Boot error: " + err.Error()
		return session.status
	}

	session.emu = emu
	session.disc = disc
	session.started = true
	session.frameCount = 0
	session.lastBackupFrame = 0
	session.status = "Running with built-in HLE BIOS: " + filepath.Base(path)
	return ""
}


var (
    configuredRenderer = "software"
    configuredUpscale = "1x"
    configuredFastBoot bool
    configuredDisplayMode = "4:3"
    configuredTextureFilter = "nearest"
)

// SetRenderer selects the active renderer: "software" or "gpu".
func SetRenderer(name string) string {
    session.Lock()
    defer session.Unlock()
    if name != "software" && name != "gpu" {
        return "Unknown renderer. Choose software or gpu."
    }
    configuredRenderer = name
    if session.emu != nil && session.started {
        if err := session.emu.SetRendererByName(name); err != nil {
            session.status = "Renderer fallback: " + err.Error()
            return session.status
        }
    }
    session.status = "Renderer: " + name
    return ""
}

// SetUpscale selects a scale supported by the current core.
func SetUpscale(name string) string {
    session.Lock()
    defer session.Unlock()
    switch name {
    case "1x", "2x", "4x", "8x", "16x":
    default:
        return "Unsupported scale. Choose 1x, 2x, 4x, 8x, or 16x."
    }
    configuredUpscale = name
    if session.emu != nil && session.started {
        if err := session.emu.SetUpscale(name); err != nil {
            session.status = "Scale change failed: " + err.Error()
            return session.status
        }
    }
    session.status = "Internal resolution: " + name
    return ""
}

// SetDisplayMode switches between aspect-correct 4:3 and full-screen stretch.
func SetDisplayMode(name string) string {
    session.Lock()
    defer session.Unlock()
    if name != "4:3" && name != "stretch" {
        return "Unknown display mode. Choose 4:3 or stretch."
    }
    configuredDisplayMode = name
    session.status = "Display mode: " + name
    return ""
}

// SetTextureFilter selects the host texture sampling filter.
func SetTextureFilter(name string) string {
    session.Lock()
    defer session.Unlock()
    if name != "nearest" && name != "bilinear" {
        return "Unsupported texture filter. Choose nearest or bilinear."
    }
    configuredTextureFilter = name
    session.status = "Texture filter: " + name
    return ""
}

// SetFastBoot toggles fast boot for the next game launch.
// The built-in HLE BIOS already skips the retail BIOS animation.
func SetFastBoot(enabled bool) {
    session.Lock()
    defer session.Unlock()
    configuredFastBoot = enabled
}

// SwapDisc changes the inserted disc without rebuilding the emulator session.
func SwapDisc(path string) string {
    session.Lock()
    defer session.Unlock()
    if session.emu == nil || !session.started {
        return "No game is currently running."
    }
    next, err := romloader.OpenDisc(path)
    if err != nil {
        return "Disc swap failed: " + err.Error()
    }
    old := session.disc
    session.emu.SetDisc(next)
    session.disc = next
    if old != nil {
        _ = old.Close()
    }
    session.status = "Disc inserted: " + filepath.Base(path)
    return ""
}

// QuitGame stops the current session and releases its disc and renderer.
func QuitGame() {
    session.Lock()
    defer session.Unlock()
    persistBackupRAMLocked()
    if session.started && session.emu != nil {
        session.emu.Close()
    }
    session.started = false
    session.emu = nil
    if session.disc != nil {
        _ = session.disc.Close()
        session.disc = nil
    }
    session.buttons = 0
    session.frameCount = 0
    session.status = "Game closed. Select a Saturn disc to begin."
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

// GetDiagnostics returns live emulation diagnostics for troubleshooting black
// screens: frame progress and the master SH-2 program counter.
func GetDiagnostics() string {
	session.Lock()
	defer session.Unlock()
	if session.emu == nil || !session.started {
		if session.status == "" {
			return "No game running. Built-in HLE BIOS is used when no external BIOS is selected."
		}
		return session.status
	}
	pc, sr := session.emu.DebugMasterPC(), session.emu.DebugMasterSR()
	return fmt.Sprintf("%s\nFrames: %d · Master PC: %08X · SR: %08X",
		session.status, session.frameCount, pc, sr)
}

type shell struct {
	frameImage *ebiten.Image
	frameWidth int
	frameHeight int
}

func (g *shell) Update() error {
	session.Lock()
	defer session.Unlock()
	if session.emu != nil && session.started {
		ensureAudioLocked()
		session.emu.SetInput(0, session.buttons)
		session.emu.RunFrame()
		session.frameCount++
		// Persist every 600 frames (~10 seconds at 60 Hz) so abrupt exits
		// lose at most a short window of progress; explicit lifecycle/quit
		// saves flush immediately.
		if session.backupPath != "" && session.frameCount-session.lastBackupFrame >= 600 {
			persistBackupRAMLocked()
		}
		if session.audioStream != nil {
			session.audioStream.Push(session.emu.GetAudioSamples())
		}
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
			// The Ebitengine screen uses the device's logical size, not the
			// Saturn framebuffer size. Upload to a correctly sized offscreen
			// image first, then scale it to fit the screen while preserving 4:3.
			if g.frameImage == nil || g.frameWidth != width || g.frameHeight != height {
				if g.frameImage != nil {
					g.frameImage.Dispose()
				}
				g.frameImage = ebiten.NewImage(width, height)
				g.frameWidth, g.frameHeight = width, height
			}
			if stride == width*4 {
				g.frameImage.WritePixels(pixels[:width*height*4])
			} else {
				tight := make([]byte, width*height*4)
				for y := 0; y < height; y++ {
					copy(tight[y*width*4:(y+1)*width*4], pixels[y*stride:y*stride+width*4])
				}
				g.frameImage.WritePixels(tight)
			}
			bounds := screen.Bounds()
			// Saturn's active picture is 4:3; many native framebuffer modes
			// use 320x224 or 352x240 pixels, so scale X/Y independently to
			// correct the pixel aspect while fitting the largest centered 4:3 area.
			targetW := float64(bounds.Dx())
			targetH := float64(bounds.Dy())
			if configuredDisplayMode != "stretch" {
				targetH = targetW * 3.0 / 4.0
				if targetH > float64(bounds.Dy()) {
					targetH = float64(bounds.Dy())
					targetW = targetH * 4.0 / 3.0
				}
			}
			drawOpts := &ebiten.DrawImageOptions{}
			if configuredTextureFilter == "bilinear" {
				drawOpts.Filter = ebiten.FilterLinear
			} else {
				drawOpts.Filter = ebiten.FilterNearest
			}
			drawOpts.GeoM.Scale(targetW/float64(width), targetH/float64(height))
			drawOpts.GeoM.Translate(
				(float64(bounds.Dx())-targetW)/2,
				(float64(bounds.Dy())-targetH)/2,
			)
			screen.DrawImage(g.frameImage, drawOpts)
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
