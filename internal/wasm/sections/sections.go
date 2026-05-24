// Package sections is the Go-side mirror of
// internal/stdlib/std/wasm/sections.fern — section composers for
// the WebAssembly Core 1.0 binary format.
//
// One step up from internal/wasm/{encode,leb128} primitives:
// each function here takes the section's logical input (a list
// of typeidxs, a list of function bodies, etc.) and emits the
// complete `id : u8 + size : uleb + body` envelope.
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html
//
// Sections covered: custom, type, function, memory, export,
// start, code, data, and a single-const-global helper for
// `global`. Import + element are deferred — same as the Lang
// side, both have fiddly variant-shaped descriptors not on the
// existing wasm backend's hot path.
package sections

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

// ---- Export-kind constants ----
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html#export-section
const (
	ExportFunc   byte = 0
	ExportTable  byte = 1
	ExportMemory byte = 2
	ExportGlobal byte = 3
)

// EncodeCustomSection emits `id 0 + uleb size + name(uleb UTF-8)
// + payload`. wasm engines ignore custom sections; tools and
// the names section hang their data here.
func EncodeCustomSection(buf []byte, name string, payload []byte) []byte {
	body := encode.PutName(nil, name)
	body = append(body, payload...)
	return encode.PutSection(buf, encode.SectionCustom, body)
}

// EncodeTypeSection emits the type section: vec(functype). Each
// functype is composed from one entry of `paramsPerType` and
// the parallel entry of `resultsPerType`. The two parallel
// slices let callers compose per-entry valtype byte sequences
// without a composite type.
func EncodeTypeSection(buf []byte, paramsPerType, resultsPerType [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(paramsPerType)))
	for i := range paramsPerType {
		body = encode.PutFuncType(body, paramsPerType[i], resultsPerType[i])
	}
	return encode.PutSection(buf, encode.SectionType, body)
}

// EncodeFunctionSection emits the function section: vec(typeidx).
// The i-th typeidx binds the i-th body in the code section to a
// type from the type section.
func EncodeFunctionSection(buf []byte, typeidxs []uint32) []byte {
	body := leb128.UlebU32(nil, uint32(len(typeidxs)))
	for _, t := range typeidxs {
		body = leb128.UlebU32(body, t)
	}
	return encode.PutSection(buf, encode.SectionFunction, body)
}

// EncodeMemorySection emits the memory section: vec(memtype).
// Wasm 1.0 allows at most one memory so this writes exactly
// one entry. memtype is a `limits` record: flag byte (0 = no
// max, 1 = with max), initial pages, and optionally max.
// maxPages < 0 means "no maximum".
func EncodeMemorySection(buf []byte, minPages uint32, maxPages int32) []byte {
	body := leb128.UlebU32(nil, 1) // exactly one memory
	if maxPages < 0 {
		body = append(body, 0x00)
		body = leb128.UlebU32(body, minPages)
	} else {
		body = append(body, 0x01)
		body = leb128.UlebU32(body, minPages)
		body = leb128.UlebU32(body, uint32(maxPages))
	}
	return encode.PutSection(buf, encode.SectionMemory, body)
}

// EncodeGlobalSection emits the global section. Each entry is
// valtype + mut + init-expr-bytes (caller-prebuilt, ending in
// the `end` opcode 0x0b). The parallel slices mirror the
// per-entry layout — the simplest shape that covers the
// const-i32 globals the existing wasm backend produces.
func EncodeGlobalSection(buf []byte, valtypes, muts []byte, initExprs [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(valtypes)))
	for i := range valtypes {
		body = append(body, valtypes[i], muts[i])
		body = append(body, initExprs[i]...)
	}
	return encode.PutSection(buf, encode.SectionGlobal, body)
}

// EncodeExportSection emits the export section: each export is
// name + kind + idx. The three parallel slices cover the three
// fields — same shape as the type section's params/results.
func EncodeExportSection(buf []byte, names []string, kinds []byte, indices []uint32) []byte {
	body := leb128.UlebU32(nil, uint32(len(names)))
	for i := range names {
		body = encode.PutName(body, names[i])
		body = append(body, kinds[i])
		body = leb128.UlebU32(body, indices[i])
	}
	return encode.PutSection(buf, encode.SectionExport, body)
}

// EncodeStartSection emits the start section. Body is a single
// funcidx (no length prefix — the section holds at most one
// value, like a Maybe<funcidx>).
func EncodeStartSection(buf []byte, funcidx uint32) []byte {
	body := leb128.UlebU32(nil, funcidx)
	return encode.PutSection(buf, encode.SectionStart, body)
}

// EncodeTableSection emits the table section (id 4): vec(table).
// Wasm 1.0 allows at most one table so this writes exactly one
// entry. tabletype is `reftype + limits`. reftype is 0x70 for
// funcref (the only reftype in MVP). Limits follow the same
// encoding as memory: flag byte (0 = no max, 1 = with max),
// initial slot count, optional max. maxSlots < 0 means
// "no maximum".
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html#table-section
func EncodeTableSection(buf []byte, minSlots uint32, maxSlots int32) []byte {
	body := leb128.UlebU32(nil, 1) // exactly one table
	body = append(body, 0x70)      // funcref
	if maxSlots < 0 {
		body = append(body, 0x00)
		body = leb128.UlebU32(body, minSlots)
	} else {
		body = append(body, 0x01)
		body = leb128.UlebU32(body, minSlots)
		body = leb128.UlebU32(body, uint32(maxSlots))
	}
	return encode.PutSection(buf, encode.SectionTable, body)
}

// EncodeElementSection emits the element section (id 9): vec(elem).
// Each segment in slice 6 is the simplest MVP shape — "active in
// table 0, with offset expression `i32.const N + end`, and a
// funcidx vector". That's element-kind 0x00 in the binary
// encoding.
//
// The two parallel slices give the per-segment offset (constant
// i32) and funcidx vector. Slot layout downstream of the offset
// is sequential, matching the wasm spec's "active" semantics.
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html#element-section
func EncodeElementSection(buf []byte, offsets []int32, funcidxs [][]uint32) []byte {
	body := leb128.UlebU32(nil, uint32(len(offsets)))
	for i, off := range offsets {
		body = append(body, 0x00) // active, table 0
		// offset expression: i32.const <off> + end (0x0b).
		body = append(body, 0x41) // i32.const
		body = leb128.SlebI32(body, off)
		body = append(body, 0x0b) // end
		// vec(funcidx)
		body = leb128.UlebU32(body, uint32(len(funcidxs[i])))
		for _, idx := range funcidxs[i] {
			body = leb128.UlebU32(body, idx)
		}
	}
	return encode.PutSection(buf, encode.SectionElement, body)
}

// EncodeCodeSection emits the code section: vec(code). Each
// entry is itself a `size : uleb + locals_vec + expr` —
// already produced by inst.PutFunctionBody (when we add it) on
// the caller side. So we just emit count + concatenation.
func EncodeCodeSection(buf []byte, bodies [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(bodies)))
	for _, b := range bodies {
		body = append(body, b...)
	}
	return encode.PutSection(buf, encode.SectionCode, body)
}

// EncodeDataSection emits the data section: vec(data). Each
// segment in wasm 1.0 is the "active" kind: memidx (always 0
// in MVP) + offset (i32.const expr) + init bytes (vec(byte)).
// The offset expression is `i32.const <offset>; end`.
func EncodeDataSection(buf []byte, offsets []int32, initBytes [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(offsets)))
	for i, off := range offsets {
		body = leb128.UlebU32(body, 0)
		body = append(body, 0x41) // i32.const
		body = leb128.SlebI32(body, off)
		body = append(body, 0x0b) // end
		seg := initBytes[i]
		body = leb128.UlebU32(body, uint32(len(seg)))
		body = append(body, seg...)
	}
	return encode.PutSection(buf, encode.SectionData, body)
}
