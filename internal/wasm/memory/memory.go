// Package memory is the Go-side mirror of
// internal/stdlib/std/wasm/memory.lang — memory load / store /
// size / grow instruction encoders for the WebAssembly Core 1.0
// binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/instructions.html#memory-instructions
//
// Every load / store carries a `memarg` immediate — an alignment
// hint plus a base offset, both uleb-encoded. memory.size /
// memory.grow each take a one-byte reserved "memory index"
// immediate (always 0 in wasm 1.0 — the multi-memory proposal
// hadn't landed when the binary format was frozen).
//
// Narrow load/store variants (load8_s, store16, etc.) only exist
// for the integer widths — IEEE-754 doesn't decompose that way.
//
// Bulk memory ops (memory.init / data.drop / memory.copy /
// memory.fill — all under the 0xFC multi-byte prefix) aren't
// included; the production wasm backend doesn't lean on them yet.
package memory

import "github.com/jakechampion/lang/internal/wasm/leb128"

// putMemarg appends the alignment + offset uleb pair every load /
// store opcode carries. Internal — the per-opcode wrappers below
// all funnel through it.
func putMemarg(buf []byte, align, offset uint32) []byte {
	buf = leb128.UlebU32(buf, align)
	return leb128.UlebU32(buf, offset)
}

// ---- Loads (0x28 - 0x35) ----
//
// `align` is the log2 of the natural alignment in bytes — 0 for
// byte-wide accesses, 1 for halfwords, 2 for words, 3 for
// doublewords. Engines use it as a hint.

func InstI32Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x28), align, offset)
}
func InstI64Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x29), align, offset)
}
func InstF32Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2a), align, offset)
}
func InstF64Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2b), align, offset)
}
func InstI32Load8S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2c), align, offset)
}
func InstI32Load8U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2d), align, offset)
}
func InstI32Load16S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2e), align, offset)
}
func InstI32Load16U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2f), align, offset)
}
func InstI64Load8S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x30), align, offset)
}
func InstI64Load8U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x31), align, offset)
}
func InstI64Load16S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x32), align, offset)
}
func InstI64Load16U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x33), align, offset)
}
func InstI64Load32S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x34), align, offset)
}
func InstI64Load32U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x35), align, offset)
}

// ---- Stores (0x36 - 0x3E) ----

func InstI32Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x36), align, offset)
}
func InstI64Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x37), align, offset)
}
func InstF32Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x38), align, offset)
}
func InstF64Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x39), align, offset)
}
func InstI32Store8(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3a), align, offset)
}
func InstI32Store16(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3b), align, offset)
}
func InstI64Store8(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3c), align, offset)
}
func InstI64Store16(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3d), align, offset)
}
func InstI64Store32(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3e), align, offset)
}

// ---- Memory size / grow ----
//
// Both carry a single reserved byte (the memory index, fixed at
// 0 in MVP wasm). memory.size pops nothing, pushes the current
// size in pages. memory.grow pops a delta and pushes the old
// size on success (or -1 on failure).

func InstMemorySize(buf []byte) []byte { return append(buf, 0x3f, 0x00) }
func InstMemoryGrow(buf []byte) []byte { return append(buf, 0x40, 0x00) }
