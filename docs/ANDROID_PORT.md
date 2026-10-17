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
- The project already depends on Ebitengine. Its mobile route uses the `ebitenmobile` binding tool plus a native Android project; a successful Go cross-compile alone does not produce an installable APK.

## Implementation milestones

1. **Android build path (in progress):** keep the desktop entry point unchanged; validate the core's Android ARM64 compilation; then add an Ebitengine mobile package and Android project shell.
2. **Controller-first frontend:** use Android's physical gamepad input; do not render virtual buttons. Handle d-pad, face buttons, shoulders/triggers, Start/Select, pause/resume, and controller disconnect/reconnect.
3. **Display and lifecycle:** landscape/fullscreen gameplay, correct aspect ratio, frame pacing, Android pause/resume and audio lifecycle.
4. **Content and persistence:** select CHD/CUE disc images and BIOS from Android's document picker; keep save data and save states in app-private storage with an export-friendly path. Confirm whether the existing disc readers can consume Android content URIs directly or need a seekable-file/cache layer.
5. **Graphics and performance:** establish a working software-rendered baseline, benchmark representative games on handheld hardware, then enable and validate an Android-capable GPU backend if practical.
6. **CI and release:** build a debug APK in GitHub Actions, publish it as a workflow artifact, and document installation and known limitations.

## Current automated check

`.github/workflows/android-core.yml` cross-compiles `./core` and `./adapter` with `GOOS=android`, `GOARCH=arm64`, and `CGO_ENABLED=0`. This is intentionally only a smoke test for Go package compatibility; it is not an APK build or a runtime test. Check the [workflow runs](https://github.com/mindspot7/Saturday/actions/workflows/android-core.yml) for the latest result.

## Acceptance checks

- The APK installs and launches on an Android handheld.
- All primary navigation and Saturn gameplay can be controlled with physical controls alone.
- A user can select a disc image and optionally a BIOS without copying copyrighted files into the repository.
- Audio, pause/resume, saves, and app lifecycle behave correctly.
- At least one representative game is tested on physical hardware; performance claims are not made from compilation alone.

## Current status

- [x] Android ARM64 core/adapter compile workflow added.
- [ ] Confirm the first workflow run passes.
- [ ] Implement the Ebitengine mobile entry point and Android host project.
- [ ] Build and install a debug APK.
- [ ] Implement disc/BIOS selection and persistence.
- [ ] Validate controls, audio, lifecycle, and performance on a physical handheld.
