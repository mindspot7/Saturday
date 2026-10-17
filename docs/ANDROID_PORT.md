# Android port

This document tracks the Android handheld port of erings (Saturday fork).

## Product requirements

- Android handhelds (AYN, Retroid, Odin and similar devices).
- Physical gamepad/controller input only; do not add on-screen controls.
- Landscape gameplay, fullscreen rendering, and navigation usable from a controller.
- Preserve the existing Saturn core and its software renderer as the compatibility baseline.
- Keep BIOS and disc images user-supplied; do not bundle copyrighted system firmware or games.

## Current implementation

- `mobile/mobile.go` registers an Ebitengine mobile game, loads user-provided BIOS bytes, opens CHD/CUE images through `romloader`, attaches the disc to `core.Emulator`, and advances the emulator one frame per update.
- The framebuffer is sent to the Ebitengine screen. This is an initial software-rendering path; color format, scaling, frame pacing, and performance still need validation on hardware.
- The native Activity provides a folder picker, copies the selected folder's files into app cache, and offers CHD/CUE selection. Copying all files together allows CUE references to sibling BIN tracks to resolve. The current importer scans only the selected folder's immediate children, not nested subfolders.
- A BIOS can be selected separately and is copied to app cache. If no BIOS is selected, the core's HLE boot path is used where supported.
- Physical d-pad/joystick axes, the standard Android A/B/X/Y face buttons, generic numbered buttons 1/2 for Saturn C/Z on compatible six-button pads, L1/L2 and R1/R2 for Saturn shoulders, and Start are mapped. Android controller button IDs vary by device, so the mappings still need validation and may need per-device remapping.
- The Activity hides Android system bars and suspends/resumes the Ebitengine view with lifecycle callbacks.
- No virtual on-screen controls, BIOS images, or game images are included.

## Known limitations

- Android UI-to-Go binding names and the full AAR/APK build must pass CI before the native frontend is considered build-verified.
- CHD and CUE/BIN loading is implemented but not yet tested with actual user content on an Android device.
- The mobile frontend now feeds the core's interleaved 44.1 kHz stereo PCM into Ebitengine audio through a bounded ring buffer. If emulation gets ahead, old samples are dropped rather than blocking the frame loop; if the audio reader gets ahead, silence fills the gap. Audible output, latency, lifecycle behavior, and sustained synchronization still require physical-device testing.
- Save states, persistent game-library metadata, BIOS management UI, pause/menu actions, controller remapping, and Android-specific graphics acceleration are not yet implemented.
- There is no physical-device smoke test yet. A successful compile is not evidence of playable performance.

## Automated Android build checks

- `.github/workflows/android-core.yml` cross-compiles `./core` and `./adapter` for Android ARM64 using the Android NDK and cgo. The core workflow passed for commit `402430ed7f599f4122a4f6cf24287ece8a0b99a2` in [run 39040123911](https://github.com/mindspot7/Saturday/actions/runs/39040123911).
- `.github/workflows/android-mobile-bind.yml` generates an Android AAR from `./mobile`, integrates it into `android/`, builds a debug APK, and uploads the AAR and APK as the `saturday-android-debug` artifact, retained for 14 days. The original scaffold passed in [run 39039451603](https://github.com/mindspot7/Saturday/actions/runs/39039451603). Builds after core integration and the native UI additions are being validated; see [latest workflow runs](https://github.com/mindspot7/Saturday/actions/workflows/android-mobile-bind.yml).
- The integrated mobile frontend and native UI passed both Android workflows at commit `5371bd3d32f2327007e823463ab4ce7c1571927f`: [core compile run 39041666730](https://github.com/mindspot7/Saturday/actions/runs/39041666730) and [AAR/APK build run 39041666728](https://github.com/mindspot7/Saturday/actions/runs/39041666728). The debug artifact from run 39041666728 contains `app-debug.apk` and `saturday.aar`. Later commits must be rechecked before their builds are called verified.

The initial core compile attempt failed because `CGO_ENABLED=0` was incompatible with `purego`'s Android dynamic-loader bridge. Another attempt failed before compilation because the Android setup action tried to install the removed legacy SDK package `tools`. The core workflow now uses the SDK preinstalled on the GitHub-hosted Ubuntu runner and installs the pinned NDK directly.

## Remaining milestones

1. **Build verification:** ensure the integrated Go binding, Java API calls, Gradle project, and APK all build in CI.
2. **Controller-first frontend:** cover face buttons, shoulders/triggers, Start/Select, analog and d-pad input, plus controller disconnect/reconnect and remapping.
3. **Display and lifecycle:** validate aspect ratio, scaling, frame pacing, Android pause/resume, and audio lifecycle.
4. **Content and persistence:** test CHD/CUE/BIN and BIOS selection on device; add persistent library and save-state management.
5. **Graphics and performance:** benchmark representative games on handheld hardware, then enable and validate an Android-capable GPU backend if practical.
6. **Release:** document installation, supported formats, BIOS expectations, and known limitations.

## Acceptance checks

- The latest APK installs and launches on an Android handheld.
- All primary navigation and Saturn gameplay can be controlled with physical controls alone.
- A user can select a disc image and optionally a BIOS without copying copyrighted files into the repository.
- Audio, pause/resume, saves, and app lifecycle behave correctly.
- At least one representative game is tested on physical hardware; performance claims are not made from compilation alone.

## Status checklist

- [x] Add Android ARM64 core/adapter compile workflow.
- [x] Confirm an Android ARM64 core/adapter compile workflow passes.
- [x] Add Ebitengine mobile entry point and native Android host project.
- [x] Confirm the initial scaffold AAR/APK workflow passes.
- [x] Connect mobile update/draw loop to the Saturn core API.
- [x] Add initial Android document-folder import, BIOS picker, and physical controller mapping.
- [x] Confirm CI passes for the integrated mobile frontend and native UI at commit `5371bd3d32f2327007e823463ab4ce7c1571927f`.
- [ ] Install and test the latest APK.
- [ ] Validate controls, audio, lifecycle, and performance on a physical handheld.
