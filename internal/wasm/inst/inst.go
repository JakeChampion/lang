// Package inst is the Go-side mirror of
// internal/stdlib/std/wasm/inst.fern — control-flow, constant,
// and variable instruction encoders for the WebAssembly Core 1.0
// binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/instructions.html
//
// Covered: constants (i32/i64/f32/f64), parametric (drop,
// select), variable (local/global get/set/tee), control flow
// (unreachable, nop, block, loop, if/else/end, br, br_if,
// return, call, call_indirect), and code-section helpers
// (PutLocalsEmpty, PutLocalsOneGroup, PutFunctionBody).
//
// Arithmetic / comparison / memory / conversion instructions
// live in sibling packages (memory, numeric, convert) so this
// file stays focused on the structural instructions a function
// body's skeleton needs.
//
// Calling convention mirrors the Lang side: every encoder takes
// a byte slice and appends. The block / loop / if forms emit
// only the *start* of the construct (opcode + blocktype); the
// caller appends the body and calls End to close it.
package inst

import (
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

// ---- Block-type encoding ----
//
// Spec: https://webassembly.github.io/spec/core/binary/instructions.html#binary-blocktype
//
// Blocktype is one of:
//   * empty type:    0x40
//   * single result: a valtype byte (0x7f/0x7e/0x7d/0x7c)
//   * type index:    a signed leb128 (33-bit space)
//
// The first two are a single byte; the typeidx form goes through
// PutBlocktypeTypeidx for the sleb encoding.
const BlocktypeEmpty byte = 0x40

func PutBlocktypeTypeidx(buf []byte, idx int32) []byte {
	return leb128.SlebI32(buf, idx)
}

// ---- Constants ----

func InstI32Const(buf []byte, v int32) []byte {
	return leb128.SlebI32(append(buf, 0x41), v)
}

func InstI64Const(buf []byte, v int64) []byte {
	return leb128.SlebI64(append(buf, 0x42), v)
}

// InstF32Const lays the IEEE-754 bit pattern as a fixed 4-byte
// little-endian u32 — no leb. The caller is responsible for
// having the bits in uint32 form.
func InstF32Const(buf []byte, bits uint32) []byte {
	return append(buf, 0x43,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
}

func InstF64Const(buf []byte, bits uint64) []byte {
	return append(buf, 0x44,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
		byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56))
}

// ---- Parametric ----

func InstDrop(buf []byte) []byte   { return append(buf, 0x1a) }
func InstSelect(buf []byte) []byte { return append(buf, 0x1b) }

// ---- Variable: locals + globals ----

func InstLocalGet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x20), idx)
}
func InstLocalSet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x21), idx)
}
func InstLocalTee(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x22), idx)
}
func InstGlobalGet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x23), idx)
}
func InstGlobalSet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x24), idx)
}

// ---- Control flow ----

func InstUnreachable(buf []byte) []byte { return append(buf, 0x00) }
func InstNop(buf []byte) []byte         { return append(buf, 0x01) }

func InstBlockStart(buf []byte, blocktype byte) []byte {
	return append(buf, 0x02, blocktype)
}
func InstLoopStart(buf []byte, blocktype byte) []byte {
	return append(buf, 0x03, blocktype)
}
func InstIfStart(buf []byte, blocktype byte) []byte {
	return append(buf, 0x04, blocktype)
}
func InstElse(buf []byte) []byte   { return append(buf, 0x05) }
func InstEnd(buf []byte) []byte    { return append(buf, 0x0b) }
func InstReturn(buf []byte) []byte { return append(buf, 0x0f) }

func InstBr(buf []byte, labelidx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x0c), labelidx)
}
func InstBrIf(buf []byte, labelidx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x0d), labelidx)
}
func InstCall(buf []byte, funcidx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x10), funcidx)
}

// InstCallIndirect carries both the expected signature
// (typeidx) and a table index. Wasm 1.0 only allows tableidx=0
// but the encoding reserves the slot for the multi-table
// proposal.
func InstCallIndirect(buf []byte, typeidx, tableidx uint32) []byte {
	buf = leb128.UlebU32(append(buf, 0x11), typeidx)
	return leb128.UlebU32(buf, tableidx)
}

// ---- Code-section helpers ----

// PutLocalsEmpty emits a zero-length locals_vec.
func PutLocalsEmpty(buf []byte) []byte {
	return leb128.UlebU32(buf, 0)
}

// PutLocalsOneGroup emits a one-group locals_vec: count locals
// of valtype vt. Covers the "function needs N i32 spill slots"
// case the existing wasm backend hits most often.
func PutLocalsOneGroup(buf []byte, count uint32, vt byte) []byte {
	buf = leb128.UlebU32(buf, 1)
	buf = leb128.UlebU32(buf, count)
	return append(buf, vt)
}

// PutFunctionBody wraps a fully-assembled function body —
// localsBytes + bodyBytes (instruction sequence, NOT including
// the final 0x0b — this helper appends it) — with the size
// prefix the code section expects.
func PutFunctionBody(buf, localsBytes, bodyBytes []byte) []byte {
	inner := make([]byte, 0, len(localsBytes)+len(bodyBytes)+1)
	inner = append(inner, localsBytes...)
	inner = append(inner, bodyBytes...)
	inner = append(inner, 0x0b)
	buf = leb128.UlebU32(buf, uint32(len(inner)))
	return append(buf, inner...)
}
