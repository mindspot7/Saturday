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

1. **Android build path (debug APK build confirmed):** the core/adapter compile for Android ARM64 passes in CI. The Ebitengine mobile binding, native Android Activity/Gradle host, and debug APK build all pass in GitHub Actions. The APK is still a frontend scaffold and has not yet been tested on a physical device.
2. **Controller-first frontend:** use Android's physical gamepad input; do not render virtual buttons. Handle d-pad, face buttons, shoulders/triggers, Start/Select, pause/resume, and controller disconnect/reconnect.
3. **Display and lifecycle:** landscape/fullscreen gameplay, correct aspect ratio, frame pacing, Android pause/resume and audio lifecycle.
4. **Content and persistence:** select CHD/CUE disc images and BIOS from Android's document picker; keep save data and save states in app-private storage with an export-friendly path. Confirm whether existing disc readers can consume Android content URIs directly or need a seekable-file/cache layer.
5. **Graphics and performance:** establish a working software-rendered baseline, benchmark representative games on handheld hardware, then enable and validate an Android-capable GPU backend if practical.
6. **CI and release:** build a debug APK in GitHub Actions, publish it as a workflow artifact, and document installation and known limitations.

## Automated Android build checks

- `.github/workflows/android-core.yml` cross-compiles `./core` and `./adapter` for Android ARM64 using the Android NDK and cgo. **Passed** in [run 39038034305](https://github.com/mindspot7/Saturday/actions/runs/39038034305).
- `.github/workflows/android-mobile-bind.yml` installs the pinned Ebitengine `ebitenmobile` tool, generates an Android AAR from `./mobile`, integrates it into the native Gradle project under `android/`, builds a debug APK, and uploads the AAR and APK as workflow artifacts. **Passed** in [run 39039451603](https://github.com/mindspot7/Saturday/actions/runs/39039451603). Download `saturday-android-debug` from the run's **Artifacts** section; the artifact contains `app-debug.apk` and `saturday.aar`.

The initial core compile attempt failed because `CGO_ENABLED=0` was incompatible with `purego`'s Android dynamic-loader bridge. The next attempt failed before compilation because the Android setup action tried to install the removed legacy SDK package `tools`. The core workflow now uses the SDK preinstalled on the GitHub-hosted Ubuntu runner and installs the pinned NDK directly.

See [core workflow runs](https://github.com/mindspot7/Saturday/actions/workflows/android-core.yml) and [mobile binding workflow runs](https://github.com/mindspot7/Saturday/actions/workflows/android-mobile-bind.yml).

## Current mobile scaffold

`mobile/mobile.go` registers an Ebitengine game with `mobile.SetGame` and displays a minimal frontend placeholder. `android/` contains a native fullscreen landscape Activity and Gradle project that hosts the generated `EbitenView` and suspends/resumes it with the Activity lifecycle. The shell deliberately does not claim to run games yet: the emulator frame loop, physical controller mapping, disc/BIOS document picker, and audio integration remain to be implemented. No virtual on-screen controls or copyrighted BIOS/game files are included.

## Acceptance checks

- The APK installs and launches on an Android handheld.
- All primary navigation and Saturn gameplay can be controlled with physical controls alone.
- A user can select a disc image and optionally a BIOS without copying copyrighted files into the repository.
- Audio, pause/resume, saves, and app lifecycle behave correctly.
- At least one representative game is tested on physical hardware; performance claims are not made from compilation alone.

## Current status

Latest successful Android build: [workflow run 39039451603](https://github.com/mindspot7/Saturday/actions/runs/39039451603), commit `2cf94501652109806849a78a1e89320633cd58db`. The generated artifact is named `saturday-android-debug` and is retained by GitHub Actions for 14 days from the run.

- [x] Add Android ARM64 core/adapter compile workflow.
- [x] Identify the initial cgo configuration error.
- [x] Replace the failing legacy SDK setup action with direct SDK/NDK setup.
- [x] Confirm the Android ARM64 core/adapter compile workflow passes.
- [x] Add an initial Ebitengine mobile entry point scaffold.
- [x] Confirm the Ebitengine Android AAR binding workflow passes.
- [x] Add the native Android Gradle host project and Activity lifecycle shell.
- [x] Confirm the binding-to-debug-APK workflow passes and produces an artifact.
- [ ] Build and install a debug APK.
- [ ] Implement disc/BIOS selection and persistence.
- [ ] Validate controls, audio, lifecycle, and performance on a physical handheld.
