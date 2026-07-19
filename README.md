# GCS for Windows ARM64 (Surface Pro 11)

Native Windows ARM64 builds of [GCS — GURPS Character Sheet](https://github.com/richardwilkes/gcs)
by Richard A. Wilkes, targeting Snapdragon X devices such as the **Surface Pro 11**.

Upstream GCS only publishes Windows x64 installers, which run on ARM devices
through emulation. As of Unison v0.93+ and pdf v1.27+, all of GCS's native
dependencies (the Skia renderer and the MuPDF PDF engine) already ship
Windows ARM64 binaries — upstream just doesn't publish an ARM64 build. This
repo fills that gap: a fully native ARM64 `gcs.exe` with **all functionality
intact**, including the PDF viewer, at full native speed and battery
efficiency.

## Install on the Surface Pro 11

1. Download `dist/gcs-5.44.0-windows-arm64.zip` from this repo (or a fresher
   build from the Actions artifacts).
2. Extract it anywhere (e.g. `C:\Program Files\GCS` or your user folder).
3. Run `gcs.exe`.

Notes:

- The executable is **not code-signed**, so Windows SmartScreen will warn on
  first launch. Click *More info → Run anyway*.
- Your library/data files live in the same locations as the official build
  (`%USERPROFILE%\GCS`), so it coexists with an existing x64 install —
  uninstall the x64 version to avoid confusion.
- Check Task Manager → Details → *Architecture* column to confirm it runs as
  ARM64 rather than emulated x64.

## What's in the box

| Piece | Status on ARM64 |
|---|---|
| GCS app (Go) | Cross/native compiled for `windows/arm64` |
| Unison UI + Skia renderer | Upstream ships `skia_windows_arm64.dll`, embedded in the exe |
| PDF viewer (MuPDF via `richardwilkes/pdf`) | Upstream ships `libmupdf_windows_arm64.a`, statically linked |
| App icon / version info / GUI manifest | Embedded via `.syso` resources |

## Building it yourself

### A) Cross-compile from Linux/macOS/WSL (what produced `dist/`)

Requires Go, Python 3, curl, unzip. Zig (installed automatically via pip)
serves as the C cross-compiler for the MuPDF cgo bindings.

```sh
./scripts/build-windows-arm64.sh          # newest GCS release
./scripts/build-windows-arm64.sh 5.44.0   # specific release
```

Output lands in `build/gcs-<version>-windows-arm64.zip`.

Three quirks the script handles for you:

- Zig's linker driver rejects `-Wl,--allow-multiple-definition` (harmless to
  drop with Zig's CRT) and GNU linker scripts, and needs `-mwindows`
  rewritten to `-Wl,--subsystem,windows`.
- The prebuilt MuPDF ARM64 library imports `_setjmpex` as a DLL function,
  but ARM64 Windows' `ucrtbase.dll` does not export it (MSVC uses compiler
  intrinsics for setjmp on ARM64), so any exe with that import dies at load
  time with *"The procedure entry point _setjmpex could not be located"*.
  The build links `scripts/setjmp_aarch64.S` — a small self-contained
  setjmp/longjmp implementation (functionally verified under QEMU) — and
  routes MuPDF's `_setjmpex`/`longjmp` imports to it.
- Upstream's Windows resource generation only runs on Windows, so the script
  uses `go-winres` to build the ARM64 `.syso` (icon, version info, manifest).

### B) GitHub Actions

`.github/workflows/build-windows-arm64.yml` has two jobs:

- **native** — builds on GitHub's `windows-11-arm` runners using upstream's
  own `build.sh --dist` with the MSYS2 CLANGARM64 toolchain, producing an
  artifact identical in layout to an official release. These runners are
  free **only for public repos**.
- **cross-zig** — the same Zig cross-build as the script, on `ubuntu-latest`;
  works on private repos too.

Trigger via *Actions → Build GCS for Windows ARM64 → Run workflow* and
optionally pin a version.

### C) Natively on the Surface Pro 11 itself

1. Install [Go](https://go.dev/dl/) (windows-arm64 installer) and
   [MSYS2](https://www.msys2.org/), then in a **CLANGARM64** shell:
   `pacman -S mingw-w64-clang-aarch64-clang zip`
2. `git clone https://github.com/richardwilkes/gcs && cd gcs`
3. Patch `build.sh` so it recognizes the CLANGARM64 shell as Windows:
   `sed -i 's/MINGW\*|MSYS\*)/MINGW*|MSYS*|CLANG*)/' build.sh`
4. `GCS_RELEASE=5.44.0 ./build.sh --dist`

## Updating to a new GCS release

Run the workflow (or the script) with the new version number — nothing here
is version-specific as long as upstream keeps shipping the ARM64 Skia DLL
and MuPDF static library (both present since mid-2026 releases). Commit the
fresh zip to `dist/` if you want it stored in-repo.

## License

GCS is © Richard A. Wilkes, licensed under the
[Mozilla Public License 2.0](https://github.com/richardwilkes/gcs/blob/master/LICENSE).
This repo contains only build tooling and unmodified builds of the upstream
source. GURPS is a trademark of Steve Jackson Games; GCS is used by
permission per upstream's README.
