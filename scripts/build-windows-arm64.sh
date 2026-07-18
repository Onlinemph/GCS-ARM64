#! /usr/bin/env bash
#
# Cross-compile GCS (GURPS Character Sheet) for Windows ARM64 from Linux or
# macOS, using Zig as the aarch64-windows C cross-compiler for the cgo parts
# (MuPDF). Produces a native ARM64 gcs.exe suitable for Snapdragon X devices
# such as the Surface Pro 11.
#
# Requirements:
#   - Go (any recent version; the required toolchain is auto-downloaded)
#   - Python 3 with pip (used to install the `ziglang` package if `zig` is
#     not already on the PATH)
#   - curl, unzip
#
# Usage:
#   ./scripts/build-windows-arm64.sh [version]
#
#   version: a GCS release tag without the leading "v" (e.g. "5.44.0"),
#            or "latest" to resolve the newest release via the Go module
#            proxy. Defaults to "latest".
#
# Output: build/gcs.exe and build/gcs-<version>-windows-arm64.zip

set -eo pipefail
trap 'echo "Build failed on line $LINENO" >&2' ERR

GCS_VERSION="${1:-latest}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$ROOT/build"
MODPROXY="${GOPROXY_BASE:-https://proxy.golang.org}"

mkdir -p "$WORK"
cd "$WORK"

# --- Resolve the zig compiler -----------------------------------------------
if command -v zig >/dev/null 2>&1; then
	ZIG="zig"
else
	if ! python3 -m ziglang version >/dev/null 2>&1; then
		echo "Installing zig (ziglang) via pip..."
		python3 -m pip install --quiet ziglang
	fi
	ZIG="python3 -m ziglang"
fi
echo "Using zig: $($ZIG version)"

# --- Fetch the GCS source via the Go module proxy ---------------------------
if [ "$GCS_VERSION" = "latest" ]; then
	GCS_VERSION="$(curl -sSL "$MODPROXY/github.com/richardwilkes/gcs/v5/@latest" | sed -n 's/.*"Version":"v\([^"]*\)".*/\1/p')"
	echo "Resolved latest GCS release: $GCS_VERSION"
fi

SRC="$WORK/gcs-$GCS_VERSION"
if [ ! -d "$SRC" ]; then
	echo "Downloading GCS v$GCS_VERSION source..."
	curl -sSL -o "$WORK/gcs-src.zip" "$MODPROXY/github.com/richardwilkes/gcs/v5/@v/v$GCS_VERSION.zip"
	unzip -q -o "$WORK/gcs-src.zip" -d "$WORK/gcs-src"
	mv "$WORK/gcs-src/github.com/richardwilkes/gcs/v5@v$GCS_VERSION" "$SRC"
	rm -rf "$WORK/gcs-src" "$WORK/gcs-src.zip"
fi

# --- CC wrapper: zig cc targeting aarch64-windows-gnu -----------------------
# Translates/drops linker args that zig's clang driver rejects:
#   -mwindows                        -> -Wl,--subsystem,windows
#   -Wl,--allow-multiple-definition  -> dropped (no duplicate symbols with zig's CRT)
#   -Wl,-T,<script>                  -> dropped (lld's COFF driver has no linker scripts)
BIN="$WORK/bin"
mkdir -p "$BIN"
cat > "$BIN/zcc" <<EOF
#!/bin/sh
args=""
for a in "\$@"; do
  case "\$a" in
    -mwindows) a="-Wl,--subsystem,windows" ;;
    -Wl,--allow-multiple-definition) continue ;;
    -Wl,-T,*) continue ;;
  esac
  args="\$args \\"\$a\\""
done
eval exec $ZIG cc -target aarch64-windows-gnu \$args
EOF
cat > "$BIN/zcxx" <<EOF
#!/bin/sh
exec $ZIG c++ -target aarch64-windows-gnu "\$@"
EOF
chmod +x "$BIN/zcc" "$BIN/zcxx"

# --- Import stub for ucrtbase!_setjmpex --------------------------------------
# The prebuilt libmupdf_windows_arm64.a shipped with richardwilkes/pdf imports
# _setjmpex from ucrtbase.dll (standard on Windows ARM64), but zig's bundled
# mingw import libraries do not expose it. Generate a one-symbol import
# library so the reference resolves; ucrtbase.dll provides it at runtime.
cat > "$WORK/ucrt_setjmpex.def" <<'EOF'
LIBRARY ucrtbase.dll
EXPORTS
_setjmpex
EOF
$ZIG dlltool -m arm64 -d "$WORK/ucrt_setjmpex.def" -l "$WORK/libucrtsetjmp.a"

# --- Windows resources (icon, version info, GUI manifest) --------------------
cd "$SRC"
export GOTOOLCHAIN=auto
export GOFLAGS=-buildvcs=false
if [ ! -f rsrc_windows_arm64.syso ]; then
	echo "Generating Windows ARM64 resources..."
	go run github.com/tc-hib/go-winres@v0.3.3 simply --arch arm64 \
		--icon pkgicons/app.png --manifest gui \
		--product-name "GURPS Character Sheet" \
		--product-version "$GCS_VERSION" --file-version "$GCS_VERSION" \
		--file-description "GURPS Character Sheet" \
		--copyright "©1998-2026 by Richard A. Wilkes" \
		--original-filename gcs.exe --out rsrc
fi

# --- Build -------------------------------------------------------------------
echo "Building GCS v$GCS_VERSION for windows/arm64..."
export GOOS=windows GOARCH=arm64 CGO_ENABLED=1
export CC="$BIN/zcc" CXX="$BIN/zcxx"
export GOEXPERIMENT=jsonv2,nodwarf5
export CGO_LDFLAGS="-L$WORK -lucrtsetjmp"
go build -trimpath \
	-ldflags "-s -w -H windowsgui -X github.com/richardwilkes/toolbox/v2/xos.AppVersion=$GCS_VERSION" \
	-o "$WORK/gcs.exe" .

# --- Package -----------------------------------------------------------------
cd "$WORK"
rm -f "gcs-$GCS_VERSION-windows-arm64.zip"
zip -9 -q "gcs-$GCS_VERSION-windows-arm64.zip" gcs.exe

python3 - "$WORK/gcs.exe" <<'EOF'
import struct, sys
d = open(sys.argv[1], "rb").read()
pe = struct.unpack_from("<I", d, 0x3C)[0]
mach = struct.unpack_from("<H", d, pe + 4)[0]
sub = struct.unpack_from("<H", d, pe + 24 + 68)[0]
assert mach == 0xAA64, f"not an ARM64 PE (machine=0x{mach:x})"
assert sub == 2, f"not a GUI-subsystem exe (subsystem={sub})"
print(f"OK: ARM64 Windows GUI executable, {len(d)/1e6:.1f} MB")
EOF

echo "Done:"
echo "  $WORK/gcs.exe"
echo "  $WORK/gcs-$GCS_VERSION-windows-arm64.zip"
