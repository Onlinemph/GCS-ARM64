// Self-contained setjmp/longjmp pair for windows/arm64.
//
// The prebuilt libmupdf_windows_arm64.a references _setjmpex and longjmp as
// dllimports. On ARM64 Windows, ucrtbase.dll does not export _setjmpex
// (MSVC uses compiler intrinsics instead), so the import cannot be satisfied
// by any system DLL. MuPDF only ever pairs its own setjmp with its own
// longjmp (fz_try/fz_throw), so a plain callee-saved register save/restore
// implementation is sufficient. Defining the __imp_ pointers here routes
// MuPDF's dllimport call sites to these functions and removes the CRT
// dependency entirely. Buffer layout follows MSVC's ARM64 _JUMP_BUFFER
// (192 bytes; jmp_buf in the mingw headers MuPDF was built with is larger).

	.text
	.globl _setjmpex
	.p2align 2
_setjmpex:
	str xzr, [x0]              // Frame = 0: never hand this buf to an unwinder
	str x1,  [x0, #8]
	stp x19, x20, [x0, #16]
	stp x21, x22, [x0, #32]
	stp x23, x24, [x0, #48]
	stp x25, x26, [x0, #64]
	stp x27, x28, [x0, #80]
	stp x29, x30, [x0, #96]    // Fp, Lr
	mov x16, sp
	str x16, [x0, #112]        // Sp
	mrs x16, fpcr
	str w16, [x0, #120]
	mrs x16, fpsr
	str w16, [x0, #124]
	stp d8,  d9,  [x0, #128]
	stp d10, d11, [x0, #144]
	stp d12, d13, [x0, #160]
	stp d14, d15, [x0, #176]
	mov w0, #0
	ret

	.globl longjmp
	.p2align 2
longjmp:
	ldp x19, x20, [x0, #16]
	ldp x21, x22, [x0, #32]
	ldp x23, x24, [x0, #48]
	ldp x25, x26, [x0, #64]
	ldp x27, x28, [x0, #80]
	ldp x29, x30, [x0, #96]
	ldr x16, [x0, #112]
	mov sp, x16
	ldr w16, [x0, #120]
	msr fpcr, x16
	ldr w16, [x0, #124]
	msr fpsr, x16
	ldp d8,  d9,  [x0, #128]
	ldp d10, d11, [x0, #144]
	ldp d12, d13, [x0, #160]
	ldp d14, d15, [x0, #176]
	cmp w1, #0
	csinc w0, w1, wzr, ne      // return val, or 1 if val == 0
	ret                        // to the Lr saved by _setjmpex

	.data
	.globl __imp__setjmpex
	.p2align 3
__imp__setjmpex:
	.quad _setjmpex

	.globl __imp_longjmp
	.p2align 3
__imp_longjmp:
	.quad longjmp
