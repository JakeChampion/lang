// Package simd encodes the fixed-width SIMD (v128) instructions of the
// WebAssembly binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/instructions.html#vector-instructions
//
// This package exists for the same reason `internal/native/x86_64/sse.go`
// grew packed-byte ops and `internal/native/arm64` grew DUP/LD1/CMEQ: the
// fused-intrinsic vector kernels of docs/ATLAS-PLATFORM-PLAN.md §3 live
// BELOW the IR, in the emitters, and an emitter can only emit what its
// assembler can encode. Nothing in Fern had ever asked wasm for a vector
// instruction before, so the encoder had none — §3.3a.
//
// ENCODING SHAPE. Every vector instruction is the prefix byte 0xFD
// followed by a *uleb128* sub-opcode. The uleb matters: sub-opcodes below
// 128 are one byte, and the ones at or above it are two (i16x8.bitmask is
// 132 → 0x84 0x01, not 0x84). Writing the sub-opcode as a raw byte
// produces a module that decodes as a DIFFERENT instruction rather than
// failing, which is why the boundary is pinned on both sides in the tests.
//
// The set here is the byte-vector core the string kernels need — load /
// store, the splats, integer equality, the bitwise ops, and the
// lane-predicate reductions (any_true / all_true / bitmask). The lane
// widths beyond i8x16 are included where they are the same table row;
// they also make the uleb boundary observable.
package simd

import "github.com/jakechampion/lang/internal/wasm/leb128"

// Prefix is the byte every vector instruction starts with.
const Prefix byte = 0xFD

// put appends the 0xFD prefix and the uleb-encoded sub-opcode.
func put(buf []byte, sub uint32) []byte {
	return leb128.UlebU32(append(buf, Prefix), sub)
}

// putMem appends a prefixed opcode plus the memarg (align, offset) pair
// the vector load / store forms carry, in the same shape as the scalar
// loads in internal/wasm/memory. `align` is the log2 of the alignment in
// bytes — 4 for a naturally aligned v128, and legally anything down to 0,
// since the vector accesses are unaligned-tolerant.
func putMem(buf []byte, sub, align, offset uint32) []byte {
	buf = put(buf, sub)
	buf = leb128.UlebU32(buf, align)
	return leb128.UlebU32(buf, offset)
}

// ---- Memory (sub-opcodes 0, 11) ----

// InstV128Load encodes `v128.load` — 16 bytes from (address on the
// stack) + offset.
func InstV128Load(buf []byte, align, offset uint32) []byte {
	return putMem(buf, 0, align, offset)
}

// InstV128Store encodes `v128.store` — the stack holds address then
// value, matching the scalar stores.
func InstV128Store(buf []byte, align, offset uint32) []byte {
	return putMem(buf, 11, align, offset)
}

// ---- Splat (15 - 18) ----
//
// Broadcast one scalar to every lane. i8x16/i16x8/i32x4 take an i32 and
// use its low bits; i64x2 takes an i64. This is the one place wasm is
// cheaper than both native targets at once: SSE2 needs movd + two
// punpckl + pshufd to splat a byte, and even NEON's single `dup` has to
// be spelled out — here it is one instruction with no lane arithmetic.

func InstI8x16Splat(buf []byte) []byte { return put(buf, 15) }
func InstI16x8Splat(buf []byte) []byte { return put(buf, 16) }
func InstI32x4Splat(buf []byte) []byte { return put(buf, 17) }
func InstI64x2Splat(buf []byte) []byte { return put(buf, 18) }

// ---- Integer compares ----
//
// Each produces an all-ones / all-zeros mask per lane. The i8x16 block is
// contiguous from 35; i16x8 continues at 45 and i32x4 at 55, but i64x2
// does NOT follow that stride — its eq/ne sit at 214/215, added by a
// later revision of the proposal. Anyone extrapolating the arithmetic
// series gets a wrong-but-valid opcode, so the i64x2 pair is spelled out.

func InstI8x16Eq(buf []byte) []byte  { return put(buf, 35) }
func InstI8x16Ne(buf []byte) []byte  { return put(buf, 36) }
func InstI8x16LtU(buf []byte) []byte { return put(buf, 38) }
func InstI8x16GtU(buf []byte) []byte { return put(buf, 40) }
func InstI8x16LeU(buf []byte) []byte { return put(buf, 42) }
func InstI8x16GeU(buf []byte) []byte { return put(buf, 44) }

func InstI16x8Eq(buf []byte) []byte { return put(buf, 45) }
func InstI16x8Ne(buf []byte) []byte { return put(buf, 46) }
func InstI32x4Eq(buf []byte) []byte { return put(buf, 55) }
func InstI32x4Ne(buf []byte) []byte { return put(buf, 56) }
func InstI64x2Eq(buf []byte) []byte { return put(buf, 214) }
func InstI64x2Ne(buf []byte) []byte { return put(buf, 215) }

// ---- Bitwise (77 - 81) ----
//
// Lane-agnostic: they operate on the 128 bits flat.

func InstV128Not(buf []byte) []byte    { return put(buf, 77) }
func InstV128And(buf []byte) []byte    { return put(buf, 78) }
func InstV128AndNot(buf []byte) []byte { return put(buf, 79) }
func InstV128Or(buf []byte) []byte     { return put(buf, 80) }
func InstV128Xor(buf []byte) []byte    { return put(buf, 81) }

// ---- Lane predicates: v128 -> i32 ----
//
// The bridge OUT of the vector domain, and the reason a byte-search
// kernel is short on wasm: `i8x16.bitmask` gathers the top bit of each of
// the 16 lanes into an i32, exactly like x86's pmovmskb — one bit per
// input byte, so `i32.ctz` on the result IS the lane index. NEON has no
// such instruction and has to go through shrn, producing four mask bits
// per byte and a divide-by-four.
//
// all_true / bitmask are per-lane-width; any_true is not (a nonzero bit
// anywhere is a nonzero bit anywhere).

func InstV128AnyTrue(buf []byte) []byte { return put(buf, 83) }

func InstI8x16AllTrue(buf []byte) []byte { return put(buf, 99) }
func InstI8x16Bitmask(buf []byte) []byte { return put(buf, 100) }
func InstI16x8AllTrue(buf []byte) []byte { return put(buf, 131) }
func InstI16x8Bitmask(buf []byte) []byte { return put(buf, 132) }
func InstI32x4AllTrue(buf []byte) []byte { return put(buf, 163) }
func InstI32x4Bitmask(buf []byte) []byte { return put(buf, 164) }
func InstI64x2AllTrue(buf []byte) []byte { return put(buf, 195) }
func InstI64x2Bitmask(buf []byte) []byte { return put(buf, 196) }
