//go:build amd64 && !noasm

#include "textflag.h"

// AVX2 kernels for SRCH-011.
//
// Each processes n elements where n is a multiple of 8 — the caller guarantees
// that and handles the remainder in Go. Eight float32 per YMM register, one FMA
// per accumulator per iteration.
//
// The pointers are read-only and never retained, which is what //go:noescape on
// the Go declarations asserts.

// horizontal sum of Y0 into the low lane of X0, clobbering X3.
#define HSUM(Yacc, Xacc)      \
	VEXTRACTF128 $1, Yacc, X3 \
	VADDPS       X3, Xacc, Xacc \
	VHADDPS      Xacc, Xacc, Xacc \
	VHADDPS      Xacc, Xacc, Xacc

// func cosinePartsAVX2(a, b *float32, n int, out *[3]float32)
//
// out[0] = dot(a,b), out[1] = dot(a,a), out[2] = dot(b,b).
//
// One pass, three accumulators. The point is not the arithmetic — SRCH-011
// measured that hoisting the query norm out of the loop saves 1.5%, because the
// bottleneck is streaming b from memory. The point is doing eight elements per
// instruction on the bytes that have to be read anyway.
TEXT ·cosinePartsAVX2(SB), NOSPLIT, $0-32
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX
	MOVQ out+24(FP), DX

	VXORPS Y0, Y0, Y0 // dot
	VXORPS Y1, Y1, Y1 // normA
	VXORPS Y2, Y2, Y2 // normB

	SHRQ $3, CX
	JZ   cosine_reduce

cosine_loop:
	VMOVUPS (SI), Y4
	VMOVUPS (DI), Y5

	VFMADD231PS Y5, Y4, Y0 // dot   += a*b
	VFMADD231PS Y4, Y4, Y1 // normA += a*a
	VFMADD231PS Y5, Y5, Y2 // normB += b*b

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ CX
	JNZ  cosine_loop

cosine_reduce:
	HSUM(Y0, X0)
	VMOVSS X0, (DX)
	HSUM(Y1, X1)
	VMOVSS X1, 4(DX)
	HSUM(Y2, X2)
	VMOVSS X2, 8(DX)

	VZEROUPPER
	RET

// func dotAVX2(a, b *float32, n int) float32
TEXT ·dotAVX2(SB), NOSPLIT, $0-28
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX

	VXORPS Y0, Y0, Y0

	SHRQ $3, CX
	JZ   dot_reduce

dot_loop:
	VMOVUPS     (SI), Y4
	VMOVUPS     (DI), Y5
	VFMADD231PS Y5, Y4, Y0

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ CX
	JNZ  dot_loop

dot_reduce:
	HSUM(Y0, X0)
	VMOVSS X0, ret+24(FP)

	VZEROUPPER
	RET

// func distSqAVX2(a, b *float32, n int) float32
//
// sum((a-b)^2). Subtract then fuse the square in, so the difference is computed
// once and squared without a second pass over it.
TEXT ·distSqAVX2(SB), NOSPLIT, $0-28
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX

	VXORPS Y0, Y0, Y0

	SHRQ $3, CX
	JZ   dist_reduce

dist_loop:
	VMOVUPS     (SI), Y4
	VMOVUPS     (DI), Y5
	VSUBPS      Y5, Y4, Y6 // d = a - b
	VFMADD231PS Y6, Y6, Y0 // sum += d*d

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ CX
	JNZ  dist_loop

dist_reduce:
	HSUM(Y0, X0)
	VMOVSS X0, ret+24(FP)

	VZEROUPPER
	RET
