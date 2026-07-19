// Command gldiag isolates the GCS-on-ARM64 rendering pipeline layer by
// layer: which opengl32.dll loads, pixel-format selection, legacy and 3.2
// contexts, raw GL clear+swap (animated colors), and finally the exact Skia
// GL surface path unison uses (clearing the window to green). Run it from a
// terminal in the GCS folder; it prints a verdict for each phase and shows a
// window that should cycle red->blue for ~4 seconds (raw GL), then turn
// green for ~4 seconds (Skia).
package main

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -L${SRCDIR} -lskia_windows_arm64 -lopengl32 -luser32 -lkernel32

#include <windows.h>
#include <stdio.h>
#include <stdbool.h>
#include "sk_capi.h"

// opengl32.dll's wgl* pixel-format/swap variants (same routing as the
// patched unison build; identical exports exist in Microsoft's and Mesa's
// opengl32.dll).
int WINAPI wglDescribePixelFormat(HDC, int, UINT, PIXELFORMATDESCRIPTOR*);
BOOL WINAPI wglSetPixelFormat(HDC, int, const PIXELFORMATDESCRIPTOR*);
BOOL WINAPI wglSwapBuffers(HDC);

// From opengl32.dll (GL 1.1 exports, ABI-correct via C).
void glClearColor(float r, float g, float b, float a);
void glClear(unsigned int mask);
const unsigned char *glGetString(unsigned int name);
unsigned int glGetError(void);
#define GL_COLOR_BUFFER_BIT 0x00004000
#define GL_VENDOR 0x1F00
#define GL_RENDERER 0x1F01
#define GL_VERSION 0x1F02
#define GL_RGBA8 0x8058

typedef HGLRC (WINAPI *createCtxAttribs_t)(HDC, HGLRC, const int*);

static void reportModule(const char *name) {
	HMODULE m = GetModuleHandleA(name);
	if (!m) { printf("  %s: NOT LOADED\n", name); return; }
	char path[MAX_PATH];
	GetModuleFileNameA(m, path, MAX_PATH);
	printf("  %s -> %s\n", name, path);
}

static LRESULT CALLBACK wndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
	if (m == WM_DESTROY) { PostQuitMessage(0); return 0; }
	return DefWindowProcA(h, m, w, l);
}

static void pump(void) {
	MSG msg;
	while (PeekMessageA(&msg, NULL, 0, 0, PM_REMOVE)) {
		TranslateMessage(&msg);
		DispatchMessageA(&msg);
	}
}

int runDiag(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	printf("== gldiag ==\n[modules at start]\n");
	reportModule("opengl32.dll");
	reportModule("skia.dll");

	WNDCLASSEXA wc = {0};
	wc.cbSize = sizeof(wc);
	wc.style = CS_HREDRAW | CS_VREDRAW | CS_OWNDC;
	wc.lpfnWndProc = wndProc;
	wc.hInstance = GetModuleHandleA(NULL);
	wc.hCursor = LoadCursor(NULL, IDC_ARROW);
	wc.lpszClassName = "gldiag";
	RegisterClassExA(&wc);
	HWND hwnd = CreateWindowExA(0, "gldiag", "GL diag - watch the colors", WS_OVERLAPPEDWINDOW | WS_VISIBLE,
		CW_USEDEFAULT, CW_USEDEFAULT, 500, 350, NULL, NULL, wc.hInstance, NULL);
	if (!hwnd) { printf("FAIL: CreateWindow\n"); return 1; }
	HDC dc = GetDC(hwnd);

	// Pixel format scan with unison's exact filter.
	PIXELFORMATDESCRIPTOR pfd = {0};
	int count = wglDescribePixelFormat(dc, 1, sizeof(pfd), NULL);
	printf("[pixel formats] count=%d\n", count);
	int chosen = 0;
	for (int i = 1; i <= count; i++) {
		wglDescribePixelFormat(dc, i, sizeof(pfd), &pfd);
		if (!(pfd.dwFlags & PFD_DRAW_TO_WINDOW) || !(pfd.dwFlags & PFD_SUPPORT_OPENGL)) continue;
		if (!(pfd.dwFlags & PFD_GENERIC_ACCELERATED) && (pfd.dwFlags & PFD_GENERIC_FORMAT)) continue;
		if (pfd.iPixelType != PFD_TYPE_RGBA) continue;
		if (!(pfd.dwFlags & PFD_DOUBLEBUFFER)) continue;
		if (pfd.cRedBits != 8 || pfd.cGreenBits != 8 || pfd.cBlueBits != 8 || pfd.cAlphaBits != 8) continue;
		if (pfd.cDepthBits != 24 || pfd.cStencilBits != 8) continue;
		chosen = i;
		break;
	}
	printf("  chosen (unison filter): %d\n", chosen);
	if (chosen == 0) { printf("FAIL: no acceptable pixel format\n"); return 1; }
	wglDescribePixelFormat(dc, chosen, sizeof(pfd), &pfd);
	if (!wglSetPixelFormat(dc, chosen, &pfd)) { printf("FAIL: wglSetPixelFormat err=%lu\n", GetLastError()); return 1; }
	printf("  wglSetPixelFormat OK\n");

	HGLRC legacy = wglCreateContext(dc);
	if (!legacy) { printf("FAIL: wglCreateContext err=%lu\n", GetLastError()); return 1; }
	if (!wglMakeCurrent(dc, legacy)) { printf("FAIL: wglMakeCurrent(legacy) err=%lu\n", GetLastError()); return 1; }
	printf("[legacy context]\n  VENDOR=%s\n  RENDERER=%s\n  VERSION=%s\n",
		glGetString(GL_VENDOR), glGetString(GL_RENDERER), glGetString(GL_VERSION));
	reportModule("opengl32.dll");

	createCtxAttribs_t createCtxAttribs = (createCtxAttribs_t)(void*)wglGetProcAddress("wglCreateContextAttribsARB");
	if (!createCtxAttribs) { printf("FAIL: no wglCreateContextAttribsARB\n"); return 1; }
	// 0x2091 = WGL_CONTEXT_MAJOR_VERSION_ARB, 0x2092 = WGL_CONTEXT_MINOR_VERSION_ARB
	const int attribs[] = {0x2091, 3, 0x2092, 2, 0};
	HGLRC modern = createCtxAttribs(dc, NULL, attribs);
	wglMakeCurrent(NULL, NULL);
	wglDeleteContext(legacy);
	if (!modern) { printf("FAIL: wglCreateContextAttribsARB err=%lu\n", GetLastError()); return 1; }
	if (!wglMakeCurrent(dc, modern)) { printf("FAIL: wglMakeCurrent(modern) err=%lu\n", GetLastError()); return 1; }
	printf("[3.2 context]\n  VENDOR=%s\n  RENDERER=%s\n  VERSION=%s\n",
		glGetString(GL_VENDOR), glGetString(GL_RENDERER), glGetString(GL_VERSION));

	// Phase: raw GL animated clear. Should visibly cycle red -> blue.
	printf("[raw GL clear+swap for ~4s] window should cycle RED -> BLUE\n");
	for (int f = 0; f < 240; f++) {
		float t = (float)f / 240.0f;
		glClearColor(1.0f - t, 0.0f, t, 1.0f);
		glClear(GL_COLOR_BUFFER_BIT);
		if (!wglSwapBuffers(dc)) printf("  SwapBuffers err=%lu (frame %d)\n", GetLastError(), f);
		pump();
		Sleep(16);
	}
	unsigned int err = glGetError();
	printf("  done; glGetError=0x%x\n", err);

	// Phase: skia path, identical to unison's surface setup.
	printf("[skia] creating native GL interface...\n");
	const gr_glinterface_t *iface = gr_glinterface_create_native_interface();
	printf("  glinterface=%p\n", (void*)iface);
	if (!iface) { printf("FAIL: gr_glinterface_create_native_interface\n"); return 1; }
	gr_direct_context_t *ctx = gr_direct_context_make_gl(iface);
	printf("  direct_context=%p\n", (void*)ctx);
	if (!ctx) { printf("FAIL: gr_direct_context_make_gl\n"); return 1; }
	RECT rc;
	GetClientRect(hwnd, &rc);
	gr_gl_framebufferinfo_t fbInfo = {0, GL_RGBA8, false};
	gr_backendrendertarget_t *rt = gr_backendrendertarget_new_gl(rc.right - rc.left, rc.bottom - rc.top, 1, 8, &fbInfo);
	printf("  backendrendertarget=%p (%ldx%ld)\n", (void*)rt, rc.right - rc.left, rc.bottom - rc.top);
	if (!rt) { printf("FAIL: gr_backendrendertarget_new_gl\n"); return 1; }
	sk_surface_props_t *props = sk_surfaceprops_new(0, 0);
	sk_surface_t *surf = sk_surface_new_backend_render_target(ctx, rt, GR_SURFACE_ORIGIN_BOTTOM_LEFT,
		SK_COLOR_TYPE_RGBA_8888, NULL, props);
	printf("  surface=%p\n", (void*)surf);
	if (!surf) { printf("FAIL: sk_surface_new_backend_render_target\n"); return 1; }
	sk_canvas_t *canvas = sk_surface_get_canvas(surf);
	printf("  canvas=%p\n", (void*)canvas);
	printf("[skia clear for ~4s] window should be solid GREEN\n");
	for (int f = 0; f < 240; f++) {
		sk_canvas_clear(canvas, 0xFF00A000);
		gr_direct_context_flush_and_submit(ctx, true);
		if (!wglSwapBuffers(dc)) printf("  SwapBuffers err=%lu (frame %d)\n", GetLastError(), f);
		pump();
		Sleep(16);
	}
	printf("  done; glGetError=0x%x\n", glGetError());
	printf("== gldiag complete ==\n");
	return 0;
}
*/
import "C"

import "os"

func main() {
	os.Exit(int(C.runDiag()))
}
