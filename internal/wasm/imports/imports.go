// Package imports is the Go-side mirror of
// internal/stdlib/std/wasm/imports.fern — import + global
// section composers for the WebAssembly Core 1.0 binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html#import-section
//
//	https://webassembly.github.io/spec/core/binary/modules.html#global-section
//
// The two outliers from the main sections package: imports
// have a four-way descriptor union and globals carry an init
// expression. Same shape as the Lang side — each descriptor
// variant has a helper returning the descriptor body bytes,
// the section composer takes parallel arrays.
package imports

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

// ---- Import-kind constants ----
const (
	ImportFunc   byte = 0
	ImportTable  byte = 1
	ImportMemory byte = 2
	ImportGlobal byte = 3
)

// ---- Mut constants for globaltype ----
const (
	MutConst byte = 0
	MutVar   byte = 1
)

// ---- Reftype constants ----
const (
	ReftypeFuncref   byte = 0x70
	ReftypeExternref byte = 0x6f
)

// ---- Per-descriptor builders ----
//
// Each returns the descriptor body bytes (without the leading
// kind byte — that goes in the parallel kinds slice).

// ImportDescFunc — body is just a typeidx.
func ImportDescFunc(typeidx uint32) []byte {
	return leb128.UlebU32(nil, typeidx)
}

// ImportDescGlobal — valtype byte + mut byte.
func ImportDescGlobal(vt, isMut byte) []byte {
	return []byte{vt, isMut}
}

// ImportDescMemory — limits record. maxPages < 0 means "no max".
func ImportDescMemory(minPages uint32, maxPages int32) []byte {
	if maxPages < 0 {
		return leb128.UlebU32([]byte{0x00}, minPages)
	}
	out := leb128.UlebU32([]byte{0x01}, minPages)
	return leb128.UlebU32(out, uint32(maxPages))
}

// ImportDescTable — reftype byte + limits record.
func ImportDescTable(reftype byte, min uint32, max int32) []byte {
	if max < 0 {
		return leb128.UlebU32([]byte{reftype, 0x00}, min)
	}
	out := leb128.UlebU32([]byte{reftype, 0x01}, min)
	return leb128.UlebU32(out, uint32(max))
}

// ---- Import section ----
//
// Body: vec(import). Four parallel slices: modules + names
// form the import's namespaced identity, kinds picks the
// descriptor variant, descBodies carries the descriptor body
// from the matching ImportDesc* helper.
func EncodeImportSection(buf []byte, modules, names []string, kinds []byte, descBodies [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(modules)))
	for i := range modules {
		body = encode.PutName(body, modules[i])
		body = encode.PutName(body, names[i])
		body = append(body, kinds[i])
		body = append(body, descBodies[i]...)
	}
	return encode.PutSection(buf, encode.SectionImport, body)
}

// ---- Global section ----
//
// Body: vec(global). Each entry: globaltype (valtype + mut) +
// init_expr. The caller supplies initExprs[i] as a complete
// constant-expression byte sequence including the terminating
// 0x0b end — typical shape is `i32.const N ; end`.
//
// (sections.EncodeGlobalSection also exists for the single-
// const-i32 shape on its own; this one provides the
// rich parallel-array variant matching imports.fern.)
func EncodeGlobalSection(buf []byte, valtypes, muts []byte, initExprs [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(valtypes)))
	for i := range valtypes {
		body = append(body, valtypes[i], muts[i])
		body = append(body, initExprs[i]...)
	}
	return encode.PutSection(buf, encode.SectionGlobal, body)
}
