# Saturday — Android port

Saturday is an Android handheld port of the erings Sega Saturn emulator, targeting devices such as AYN Odin and Retroid. The Android frontend is being developed in this branch.

## Android frontend

- Native Android app with fullscreen gameplay.
- Physical gamepad/controller input; no virtual on-screen controls.
- User-selected Saturn disc images in CHD or CUE/BIN format.
- Built-in HLE BIOS is selected automatically when no external BIOS is loaded; an external Saturn BIOS remains optional for compatibility. No BIOS or game images are included.
- Ebitengine-based presentation and audio output.

## Build

The Android project is in `android/`. Automated Android builds are defined in:

- `.github/workflows/android-core.yml` — cross-compiles the emulator core for Android ARM64.
- `.github/workflows/android-mobile-bind.yml` — generates the Ebitengine Android binding, builds a debug APK, and uploads the APK/AAR artifact.

See [docs/ANDROID_PORT.md](docs/ANDROID_PORT.md) for implementation details, known limitations, and build instructions.

## Rendering

The core includes CPU software rendering and GPU-rendering infrastructure for other host platforms. The Android frontend currently consumes the core framebuffer and presents it through Ebitengine. Android-specific GPU acceleration is not yet implemented or verified.

## Disc images and BIOS

Supported disc formats in the Android frontend are CHD and CUE with their referenced BIN tracks. A Saturn BIOS is optional where the core's HLE boot path supports the game. Source your own BIOS and game images; neither is distributed in this repository.

## Project status

This port is a work in progress. Successful compilation does not guarantee that a game boots, renders correctly, produces audio, or runs at full speed on a physical device. Real-device testing is still required.
