// Package encode is the Go-side mirror of
// internal/stdlib/std/wasm/encode.fern — container-level
// binary writers for the WebAssembly Core 1.0 binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html
//
// Same function set, same semantics as the Lang side. Sits on
// top of internal/wasm/leb128. Pair this with later mirrors
// (sections, inst, etc.) to assemble a complete binary module
// without going through WAT text.
//
// Calling convention mirrors the Lang side: every writer takes
// a byte slice, appends to it, and returns the (possibly
// reallocated) slice. No I/O, no globals.
package encode

import "github.com/jakechampion/lang/internal/wasm/leb128"

// ---- Valtype byte constants ----
//
// Spec: https://webassembly.github.io/spec/core/binary/types.html#binary-valtype
const (
	ValtypeI32 byte = 0x7f
	ValtypeI64 byte = 0x7e
	ValtypeF32 byte = 0x7d
	ValtypeF64 byte = 0x7c
)

// ---- Section ID constants ----
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html#sections
const (
	SectionCustom   byte = 0
	SectionType     byte = 1
	SectionImport   byte = 2
	SectionFunction byte = 3
	SectionTable    byte = 4
	SectionMemory   byte = 5
	SectionGlobal   byte = 6
	SectionExport   byte = 7
	SectionStart    byte = 8
	SectionElement  byte = 9
	SectionCode     byte = 10
	SectionData     byte = 11
)

// PutBytes appends src to buf and returns the extended buffer.
// Mirrors put_bytes; equivalent to `append(buf, src...)` but
// kept as a named function so the Lang ↔ Go correspondence is
// 1:1.
func PutBytes(buf, src []byte) []byte {
	return append(buf, src...)
}

// PutU32LE appends v as a fixed 4-byte little-endian uint32.
// Used for the wasm preamble's version field — the only
// fixed-width integer in the format (everything else is leb).
func PutU32LE(buf []byte, v uint32) []byte {
	return append(buf,
		byte(v),
		byte(v>>8),
		byte(v>>16),
		byte(v>>24))
}

// PutModuleHeader appends the wasm preamble: magic "\0asm"
// (0x00 0x61 0x73 0x6d) followed by version 1 as fixed-width
// u32 LE (0x01 0x00 0x00 0x00). Always 8 bytes.
func PutModuleHeader(buf []byte) []byte {
	buf = append(buf, 0x00, 'a', 's', 'm')
	return PutU32LE(buf, 1)
}

// PutName appends a wasm "name" — UTF-8 bytes prefixed by their
// uleb-encoded length. Spec calls this `vec(byte)`, used for
// import / export descriptors and the names custom section.
func PutName(buf []byte, s string) []byte {
	buf = leb128.UlebU32(buf, uint32(len(s)))
	return append(buf, s...)
}

// PutSection wraps a precomputed body with its section header:
// `id : u8` + `size : uleb` + body. The caller builds the body
// in its own buffer first so the size prefix can be written
// before the body bytes.
func PutSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = leb128.UlebU32(buf, uint32(len(body)))
	return append(buf, body...)
}

// PutFuncType appends a function type to a type-section body:
// `0x60` + `vec(params)` + `vec(results)`. Each param / result
// element is a single valtype byte. The caller composes
// `params` / `results` from the Valtype* constants above.
//
// Spec: https://webassembly.github.io/spec/core/binary/types.html#function-types
func PutFuncType(buf, params, results []byte) []byte {
	buf = append(buf, 0x60)
	buf = leb128.UlebU32(buf, uint32(len(params)))
	buf = append(buf, params...)
	buf = leb128.UlebU32(buf, uint32(len(results)))
	return append(buf, results...)
}
