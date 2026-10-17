# Android port

This document tracks the Android handheld port of erings (Saturday fork).

## Product requirements

- Android handhelds (AYN, Retroid, Odin and similar devices).
- Physical gamepad/controller input only; do not add on-screen controls.
- Landscape gameplay, fullscreen rendering, and navigation usable from a controller.
- Preserve the existing Saturn core and its software renderer as the compatibility baseline.
- Keep BIOS and disc images user-supplied; do not bundle copyrighted system firmware or games.

## Findings from the source review

- The emulation core is in `core/`; `adapter/` exposes it through eblitui's `coreif` interfaces.
- The current executable at `cmd/desktop/main.go` depends on `github.com/user-none/eblitui/desktop`, so it is not itself an Android entry point.
- The existing desktop build workflow and Makefile package desktop platforms only.
- `core/renderer_software.go` provides a CPU renderer. This is the initial compatibility baseline, but performance must be measured on real devices.
- The host GPU backend is platform-specific. Android GPU support must be validated independently; do not assume the Linux Vulkan backend is a drop-in Android backend.
- The project already depends on Ebitengine and gomobile-related packages. A mobile app still needs a dedicated Ebitengine mobile entry point and native Android project; a Go cross-compile alone does not produce an installable APK.

## Implementation milestones

1. **Android build path (in progress):** validate the core's Android ARM64 compilation; then add an Ebitengine mobile package and Android project shell without changing the desktop entry point.
2. **Controller-first frontend:** use Android's physical gamepad input; do not render virtual buttons. Handle d-pad, face buttons, shoulders/triggers, Start/Select, pause/resume, and controller disconnect/reconnect.
3. **Display and lifecycle:** landscape/fullscreen gameplay, correct aspect ratio, frame pacing, Android pause/resume and audio lifecycle.
4. **Content and persistence:** select CHD/CUE disc images and BIOS from Android's document picker; keep save data and save states in app-private storage with an export-friendly path. Confirm whether existing disc readers can consume Android content URIs directly or need a seekable-file/cache layer.
5. **Graphics and performance:** establish a working software-rendered baseline, benchmark representative games on handheld hardware, then enable and validate an Android-capable GPU backend if practical.
6. **CI and release:** build a debug APK in GitHub Actions, publish it as a workflow artifact, and document installation and known limitations.

## Automated Android compile check

`.github/workflows/android-core.yml` cross-compiles `./core` and `./adapter` for Android ARM64 using the Android NDK and cgo.

The initial attempt failed because `CGO_ENABLED=0` was incompatible with `purego`'s Android dynamic-loader bridge. The second attempt enabled cgo but failed before compilation: `android-actions/setup-android@v3` tried to install the removed legacy SDK package `tools`. The workflow now uses the Android SDK already installed on the GitHub-hosted Ubuntu runner, accepts SDK licenses, and installs the pinned NDK directly. This updated workflow must still complete successfully before Android compilation can be marked as passing.

See [workflow runs](https://github.com/mindspot7/Saturday/actions/workflows/android-core.yml).

## Acceptance checks

- The APK installs and launches on an Android handheld.
- All primary navigation and Saturn gameplay can be controlled with physical controls alone.
- A user can select a disc image and optionally a BIOS without copying copyrighted files into the repository.
- Audio, pause/resume, saves, and app lifecycle behave correctly.
- At least one representative game is tested on physical hardware; performance claims are not made from compilation alone.

## Current status

- [x] Add Android ARM64 core/adapter compile workflow.
- [x] Identify the initial cgo configuration error.
- [x] Replace the failing legacy SDK setup action with direct SDK/NDK setup.
- [ ] Confirm the corrected workflow passes.
- [ ] Implement the Ebitengine mobile entry point and Android host project.
- [ ] Build and install a debug APK.
- [ ] Implement disc/BIOS selection and persistence.
- [ ] Validate controls, audio, lifecycle, and performance on a physical handheld.
