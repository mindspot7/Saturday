# Android port

This document tracks the Android handheld port of erings (Saturday fork).

## Product requirements

- Android handhelds (AYN, Retroid, Odin and similar devices).
- Physical gamepad/controller input only; do not add on-screen controls.
- Landscape gameplay, fullscreen rendering, and navigation usable from a controller.
- Preserve the existing Saturn core and its software renderer as the compatibility baseline.
- Keep BIOS and disc images user-supplied; do not bundle copyrighted system firmware or games.

## Findings from the initial source review

- The emulation core is in `core/`; `adapter/` exposes it through eblitui's `coreif` interfaces.
- The current executable at `cmd/desktop/main.go` depends on `github.com/user-none/eblitui/desktop`, so it is not itself an Android entry point.
- The current build workflow and Makefile only package desktop platforms.
- `core/renderer_software.go` provides a CPU renderer. This is a useful initial fallback for Android, but its speed on real devices must be measured before choosing defaults.
- The host GPU backend is platform-specific. Android GPU support must be validated independently; do not assume the Linux Vulkan backend is a drop-in Android backend.

## Implementation milestones

1. **Android build path:** add a dedicated Android entry point and reproducible Android build tooling without changing the desktop entry point.
2. **Controller-first frontend:** use Android's physical gamepad input; do not render virtual buttons. Handle d-pad, face buttons, shoulders/triggers, Start/Select, pause/resume, and controller disconnect/reconnect.
3. **Display and lifecycle:** landscape/fullscreen gameplay, correct aspect ratio, frame pacing, Android pause/resume and audio lifecycle.
4. **Content and persistence:** select CHD/CUE disc images and BIOS from Android's document picker; keep save data and save states in app-private storage with an export-friendly path.
5. **Graphics and performance:** establish a working software-rendered baseline, benchmark representative games on handheld hardware, then enable and validate an Android-capable GPU backend if practical.
6. **CI and release:** build a debug APK in GitHub Actions, publish it as a workflow artifact, and document installation and known limitations.

## Acceptance checks

- The APK installs and launches on an Android handheld.
- All primary navigation and Saturn gameplay can be controlled with physical controls alone.
- A user can select a disc image and optionally a BIOS without copying copyrighted files into the repository.
- Audio, pause/resume, saves, and app lifecycle behave correctly.
- At least one representative game is tested on physical hardware; performance claims are not made from compilation alone.

## Current status

Planning and repository inspection only. The Android frontend, APK workflow, and device testing have not yet been implemented.
