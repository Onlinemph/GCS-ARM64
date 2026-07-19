#! /usr/bin/env bash
#
# Patch a writable copy of the richardwilkes/unison module so GCS works
# correctly on Windows ARM64. Two independent fixes:
#
# 1. WGL routing: unison sets the pixel format / swaps buffers via gdi32.dll
#    (always the system ICD) while creating contexts via opengl32.dll. Route
#    everything through opengl32.dll — Microsoft's opengl32 exports identical
#    wgl* variants, so this is behavior-neutral normally, and it lets an
#    app-local Mesa opengl32.dll fully take over on machines whose system
#    OpenGL is broken (e.g. GLon12 on some Snapdragon devices).
#
# 2. Skia binding: on Windows, unison calls the Skia DLL via Go's
#    syscall.Proc.Call, passing floats as raw bits in integer registers. That
#    works on x64 only because Go's runtime mirrors the first 4 args into
#    XMM registers and the MS x64 ABI uses positional slots. On ARM64 the
#    integer and float argument sequences are separate, Go's runtime does not
#    mirror into SIMD registers (see the TODO in
#    internal/runtime/syscall/windows/asm_windows_arm64.s), so every
#    float-taking Skia call silently receives garbage: the app renders a
#    blank window. Switch windows/arm64 to unison's cgo Skia binding (used on
#    every other platform), where the C compiler performs correct AAPCS64
#    marshaling, linking against an import library generated from the
#    shipped skia_windows_arm64.dll. The DLL must then be distributed next
#    to gcs.exe, since the syscall path's embed-and-extract loader is no
#    longer compiled in.
#
# Usage: patch-unison-arm64.sh <unison-dir> <dlltool-cmd...>
#   e.g. patch-unison-arm64.sh build/unison-patched zig dlltool
#        patch-unison-arm64.sh ../unison-patched llvm-dlltool

set -eo pipefail
trap 'echo "patch-unison-arm64.sh failed on line $LINENO" >&2' ERR

UNISON="$1"
shift
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
SK="$UNISON/internal/skia"

# --- Fix 1: WGL routing ------------------------------------------------------
GDI="$UNISON/internal/w32/gdi32_windows.go"
sed -i.bak \
	-e 's/gdi32\.NewProc("DescribePixelFormat")/opengl32.NewProc("wglDescribePixelFormat")/' \
	-e 's/gdi32\.NewProc("SetPixelFormat")/opengl32.NewProc("wglSetPixelFormat")/' \
	-e 's/gdi32\.NewProc("SwapBuffers")/opengl32.NewProc("wglSwapBuffers")/' \
	"$GDI" && rm -f "$GDI.bak"
grep -q 'wglSetPixelFormat' "$GDI" || { echo "WGL patch failed to apply" >&2; exit 1; }

# --- Fix 2: cgo Skia binding on windows/arm64 --------------------------------
# Import library from the DLL's export table.
(cd "$SCRIPTS/gen-dll-def" && GOOS= GOARCH= CGO_ENABLED= go run . "$SK/skia_windows_arm64.dll") > "$SK/skia_windows_arm64.def"
"$@" -m arm64 -d "$SK/skia_windows_arm64.def" -l "$SK/libskia_windows_arm64.a"

# The syscall-based binding becomes windows/amd64-only.
sed -i.bak '1s|^|//go:build amd64\n\n|' "$SK/skia_windows.go" && rm -f "$SK/skia_windows.go.bak"
sed -i.bak '1s|^|//go:build amd64\n\n|' "$SK/skia_windows_arm64.go" && rm -f "$SK/skia_windows_arm64.go.bak"

# The cgo binding gains windows/arm64, with link flags for the import lib.
sed -i.bak \
	-e 's|^//go:build !windows$|//go:build !windows \|\| arm64|' \
	-e 's|^#cgo CFLAGS: -I${SRCDIR}$|#cgo CFLAGS: -I${SRCDIR}\n#cgo windows,arm64 LDFLAGS: -L${SRCDIR} -lskia_windows_arm64|' \
	-e '/^const ColorTypeN32 = ColorTypeRGBA8888$/d' \
	"$SK/skia_other.go" && rm -f "$SK/skia_other.go.bak"
grep -q 'lskia_windows_arm64' "$SK/skia_other.go" || { echo "skia cgo patch failed to apply" >&2; exit 1; }

# The arm64 Skia DLL was built without the Windows GL loader
# (GrGLMakeNativeInterface is a stub returning null — the amd64 DLL contains
# the opengl32.dll/wglGetProcAddress machinery, the arm64 DLL does not), so
# native interface creation must be replaced with gr_glmake_assembled_interface
# fed by a proc-getter implemented here. Rename the stock implementation and
# add per-platform wrappers.
sed -i.bak 's|^func GLInterfaceCreateNativeInterface() GLInterface {$|func glInterfaceCreateNativeInterfaceDefault() GLInterface {|' \
	"$SK/skia_other.go" && rm -f "$SK/skia_other.go.bak"
grep -q 'glInterfaceCreateNativeInterfaceDefault' "$SK/skia_other.go" || { echo "iface rename failed" >&2; exit 1; }

cat > "$SK/skia_iface_notwin.go" <<'EOF'
//go:build !windows

package skia

func GLInterfaceCreateNativeInterface() GLInterface {
	return glInterfaceCreateNativeInterfaceDefault()
}
EOF

cat > "$SK/skia_iface_windows_arm64.go" <<'EOF'
package skia

/*
#include <windows.h>
#include "sk_capi.h"

// The skia_windows_arm64.dll build lacks the WGL-based native interface
// factory (it returns NULL), so assemble the GL interface ourselves: resolve
// core entry points from opengl32.dll and extensions via wglGetProcAddress.
// A GL context must be current when this runs, which unison guarantees.
static gr_glfunc_ptr goWinGLGetProc(void *ctx, const char *name) {
	static HMODULE gl;
	typedef PROC (WINAPI *wglGetProcAddress_t)(LPCSTR);
	static wglGetProcAddress_t wgpa;
	if (!gl) {
		gl = LoadLibraryA("opengl32.dll");
		if (gl) wgpa = (wglGetProcAddress_t)(void*)GetProcAddress(gl, "wglGetProcAddress");
	}
	if (!gl) return NULL;
	PROC p = GetProcAddress(gl, name);
	if (!p && wgpa) {
		p = wgpa(name);
		INT_PTR v = (INT_PTR)p;
		if (v == 0 || v == 1 || v == 2 || v == 3 || v == -1) p = NULL;
	}
	return (gr_glfunc_ptr)(void*)p;
}

static const gr_glinterface_t *makeAssembledGLInterface(void) {
	return gr_glmake_assembled_interface(NULL, goWinGLGetProc);
}
*/
import "C"

func GLInterfaceCreateNativeInterface() GLInterface {
	if iface := GLInterface(C.makeAssembledGLInterface()); iface != nil {
		return iface
	}
	return glInterfaceCreateNativeInterfaceDefault()
}
EOF

# ColorTypeN32 moves to per-platform files: the removed const for non-windows,
# and BGRA (matching the windows/amd64 syscall binding) for windows/arm64.
cat > "$SK/skia_n32_notwin.go" <<'EOF'
//go:build !windows

package skia

const ColorTypeN32 = ColorTypeRGBA8888
EOF
cat > "$SK/skia_n32_windows_arm64.go" <<'EOF'
package skia

const ColorTypeN32 = ColorTypeBGRA8888
EOF

echo "unison patched for windows/arm64 at $UNISON"
