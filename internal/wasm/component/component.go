// Package component is the Go-side counterpart of the Lang stdlib
// `std/wasm/component` module. It writes Component Model binary
// envelopes around already-encoded core wasm modules.
//
// Spec: https://github.com/WebAssembly/component-model/blob/main/design/mvp/Binary.md
//
// This package owns the encoder primitives used by the production
// driver (cmd/fern) when it composes a preview-2-native component
// without shelling out to `wasm-tools component new`. The Lang
// stdlib version is the long-term self-hosting target; this Go
// version is the bridge that lets us retire the wasm-tools shell-
// out today, before the compiler is self-hosted.
//
// The two implementations are intentionally kept byte-for-byte
// equivalent: when one ships a new section composer, the other
// follows. component_test.go pins this with side-by-side checks
// against `wasm-tools parse` output for representative shapes.
package component

import (
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

// Component-Model binary section IDs (matches the constants in
// std/wasm/component.fern's section_*).
const (
	SectionCustom       = 0
	SectionCoreModule   = 1
	SectionCoreInstance = 2
	SectionCoreType     = 3
	SectionComponent    = 4
	SectionInstance     = 5
	SectionAlias        = 6
	SectionType         = 7
	SectionCanon        = 8
	SectionStart        = 9
	SectionImport       = 10
	SectionExport       = 11
)

// Core-sort discriminator bytes (used inside core-sortidx in
// alias and core-instance entries).
const (
	CoreSortFunc     = 0x00
	CoreSortTable    = 0x01
	CoreSortMemory   = 0x02
	CoreSortGlobal   = 0x03
	CoreSortType     = 0x10
	CoreSortModule   = 0x11
	CoreSortInstance = 0x12
)

// Component primitive valtype bytes. Distinct numbering from the
// core wasm valtypes (which live in internal/wasm/encode): core i32
// is 0x7f, component bool is also 0x7f. Each space is parsed
// independently.
const (
	CValtypeBool   = 0x7f
	CValtypeS8     = 0x7e
	CValtypeU8     = 0x7d
	CValtypeS16    = 0x7c
	CValtypeU16    = 0x7b
	CValtypeS32    = 0x7a
	CValtypeU32    = 0x79
	CValtypeS64    = 0x78
	CValtypeU64    = 0x77
	CValtypeF32    = 0x76
	CValtypeF64    = 0x75
	CValtypeChar   = 0x74
	CValtypeString = 0x73
)

// PutComponentHeader appends the Component Model preamble:
// `\0asm` + version 0x000d + layer 0x0001. Always 8 bytes.
// Layer = 0x01 is what distinguishes a component from a core module
// (whose version field is 0x01000000 and has no layer field).
func PutComponentHeader(buf []byte) []byte {
	return append(buf,
		0x00, 0x61, 0x73, 0x6d, // "\0asm"
		0x0d, 0x00, // version 0x000d
		0x01, 0x00, // layer 0x0001 (component)
	)
}

// PutCoreModuleSection wraps a complete core wasm module as a
// core-module section: `id : u8 + size : uleb + body`. `core` is
// the entire core module starting with its own preamble.
func PutCoreModuleSection(buf, core []byte) []byte {
	buf = append(buf, SectionCoreModule)
	buf = leb128.UlebU64(buf, uint64(len(core)))
	buf = append(buf, core...)
	return buf
}

// PutComponentSection embeds a nested component (section id 4). `comp`
// is an entire component starting with its own preamble — the
// component-level analogue of PutCoreModuleSection. Used to bundle a
// provider component inside a consumer (e.g. an async-export provider
// the consumer awaits), so the result is a single self-contained
// runnable component with no external link step.
func PutComponentSection(buf, comp []byte) []byte {
	buf = append(buf, SectionComponent)
	buf = leb128.UlebU64(buf, uint64(len(comp)))
	buf = append(buf, comp...)
	return buf
}

// PutInstanceSectionInstantiateComponent emits a component-level
// instance section (id 5) with one "instantiate" entry that
// instantiates an embedded component (by component index) with no
// import args — the component-level analogue of
// PutCoreInstanceSectionInstantiate. (A component with imports would
// pass `(name, sort, idx)` arg triples; the no-import form covers a
// self-contained provider.)
func PutInstanceSectionInstantiateComponent(buf []byte, componentIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x00)      // form: instantiate
	body = leb128.UlebU64(body, uint64(componentIdx))
	body = leb128.UlebU64(body, 0) // vec(0) args
	return wrapSection(buf, SectionInstance, body)
}

// PutComponentImportSectionFuncs emits a component-level import section
// declaring N function imports — the i-th named `names[i]` with the
// component functype at `typeidxs[i]`. Used by the async-import sibling
// composition: the consumer is a nested component that IMPORTS each async
// function it awaits (rather than lowering a bundled provider into itself),
// so the outer component can link a sibling provider instance to it and the
// consumer→provider call is a clean cross-instance call. wasmtime v46's
// component-model reentrancy check rejects the older same-instance lower.
// Func externdesc kind = 0x01 (mirrors the alias func sort).
func PutComponentImportSectionFuncs(buf []byte, names []string, typeidxs []uint32) []byte {
	if len(names) != len(typeidxs) {
		panic("component: names and typeidxs must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, uint64(len(names)))
	for i := range names {
		body = append(body, 0x00) // importname kind = label
		body = putName(body, names[i])
		body = append(body, 0x01) // externdesc kind = func
		body = leb128.UlebU64(body, uint64(typeidxs[i]))
	}
	return wrapSection(buf, SectionImport, body)
}

// PutInstanceSectionInstantiateComponentWithFuncArgs instantiates the
// component at `componentIdx`, supplying `argNames[i]` = the component func
// at `funcIdxs[i]`. The component-level analogue of
// PutCoreInstanceSectionInstantiateWithInstanceArgs for func (sort 0x01)
// arguments — used to feed sibling provider funcs into the consumer
// component of the async-import sibling composition.
func PutInstanceSectionInstantiateComponentWithFuncArgs(buf []byte, componentIdx uint32, argNames []string, funcIdxs []uint32) []byte {
	if len(argNames) != len(funcIdxs) {
		panic("component: argNames and funcIdxs must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x00)      // form: instantiate
	body = leb128.UlebU64(body, uint64(componentIdx))
	body = leb128.UlebU64(body, uint64(len(argNames))) // vec(args)
	for i := range argNames {
		body = putName(body, argNames[i])
		body = append(body, 0x01) // sort = func
		body = leb128.UlebU64(body, uint64(funcIdxs[i]))
	}
	return wrapSection(buf, SectionInstance, body)
}

// putName appends a uleb-prefixed UTF-8 name (the component-model
// name encoding, identical to core wasm names).
func putName(buf []byte, s string) []byte {
	buf = leb128.UlebU64(buf, uint64(len(s)))
	buf = append(buf, s...)
	return buf
}

// wrapSection wraps `body` in a section header: `id + uleb_size + body`.
func wrapSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = leb128.UlebU64(buf, uint64(len(body)))
	buf = append(buf, body...)
	return buf
}

// FixupModuleForNI32NoResult returns the fixup core module for an
// imported func type of `(i32 × nparams) -> ()`. Paired with
// TrampolineModuleForNI32NoResult; the func + table imports and the
// elem segment that installs the lowered func into table[0] are
// independent of the param count.
func FixupModuleForNI32NoResult(nparams int) []byte {
	return FixupModuleForParamsNoResult(repeatByte(0x7f, nparams))
}

// FixupModuleForParamsNoResult is the valtype-generalised
// FixupModuleForNI32NoResult: the imported func type is
// `(paramValtypes...) -> ()`, letting callers express mixed
// signatures (e.g. the `(i32, i64, i32) -> ()` shape of the
// canon-lowered blocking-read method). Everything past the type
// section — the func + table imports and the elem segment that
// installs the lowered func into table[0] — is independent of the
// param valtypes.
func FixupModuleForParamsNoResult(paramValtypes []byte) []byte {
	// core wasm preamble
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// type section: vec(1) functype (paramValtypes...) -> ()
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = append(typeBody, 0x00) // vec(0) results
	out = appendCoreSection(out, 0x01, typeBody)
	// import section: vec(2) — ("" "0") func 0, ("" "$imports") table
	importBody := []byte{
		0x02,
		0x00, 0x01, '0', 0x00, 0x00,
		0x00, 0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x70, 0x01, 0x01, 0x01,
	}
	out = appendCoreSection(out, 0x02, importBody)
	// element section: active segment, table 0, offset i32.const 0,
	// vec(1) funcidx [0].
	elemBody := []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x00}
	out = appendCoreSection(out, 0x09, elemBody)
	return out
}

// appendCoreSection appends a core-wasm section (id byte + uleb
// size + body) to a core module being assembled. Distinct from
// wrapSection, which targets the component-level section space
// (same wire shape, kept separate for readability at call sites).
func appendCoreSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = leb128.UlebU64(buf, uint64(len(body)))
	return append(buf, body...)
}

// TrampolineModuleForNI32NoResult returns the trampoline core
// module whose exported func type is `(i32 × nparams) -> ()`; the
// body forwards all nparams to a 1-entry funcref-table
// call_indirect. Paired with FixupModuleForNI32NoResult.
func TrampolineModuleForNI32NoResult(nparams int) []byte {
	return TrampolineModuleForParamsNoResult(repeatByte(0x7f, nparams))
}

// TrampolineModuleForParamsNoResult is the valtype-generalised
// TrampolineModuleForNI32NoResult: the exported func type is
// `(paramValtypes...) -> ()` and the body forwards every param to
// a 1-entry funcref-table call_indirect. Lets callers express
// mixed signatures — e.g. the canon-lowered blocking-read ABI
// `(self: i32, len: i64, ret_ptr: i32) -> ()`.
func TrampolineModuleForParamsNoResult(paramValtypes []byte) []byte {
	// core wasm preamble
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// type section: vec(1) functype (paramValtypes...) -> ()
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = append(typeBody, 0x00)
	out = appendCoreSection(out, 0x01, typeBody)
	// function section: vec(1) typeidx 0
	out = appendCoreSection(out, 0x03, []byte{0x01, 0x00})
	// table section: vec(1) funcref table, min=1, max=1
	out = appendCoreSection(out, 0x04, []byte{0x01, 0x70, 0x01, 0x01, 0x01})
	// export section: vec(2) — "0" func 0, "$imports" table 0
	exportBody := []byte{
		0x02,
		0x01, '0', 0x00, 0x00,
		0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x00,
	}
	out = appendCoreSection(out, 0x07, exportBody)
	// code section: vec(1) body — vec(0) locals, local.get 0..n-1,
	// i32.const 0, call_indirect type 0 table 0, end.
	funcBody := []byte{0x00}
	for i := range paramValtypes {
		funcBody = append(funcBody, 0x20)
		funcBody = leb128.UlebU64(funcBody, uint64(i))
	}
	funcBody = append(funcBody, 0x41, 0x00, 0x11, 0x00, 0x00, 0x0b)
	codeBody := []byte{0x01}
	codeBody = leb128.UlebU64(codeBody, uint64(len(funcBody)))
	codeBody = append(codeBody, funcBody...)
	out = appendCoreSection(out, 0x0a, codeBody)
	return out
}

// TrampolineModuleForParamsResults is TrampolineModuleForParamsNoResult with a
// non-empty result vector: the exported func type is `(paramValtypes...) ->
// (resultValtypes...)`, and the body forwards every param to the funcref-table
// call_indirect whose result flows straight through. Used to wire a
// memory-param import that still returns a flat scalar (P4c — e.g. a custom
// `func(string) -> u32`); the result-less variant only handled WASI's
// retptr-returning imports. resultValtypes empty falls back to the NoResult
// shape (identical bytes).
func TrampolineModuleForParamsResults(paramValtypes, resultValtypes []byte) []byte {
	if len(resultValtypes) == 0 {
		return TrampolineModuleForParamsNoResult(paramValtypes)
	}
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = leb128.UlebU64(typeBody, uint64(len(resultValtypes)))
	typeBody = append(typeBody, resultValtypes...)
	out = appendCoreSection(out, 0x01, typeBody)
	out = appendCoreSection(out, 0x03, []byte{0x01, 0x00})
	out = appendCoreSection(out, 0x04, []byte{0x01, 0x70, 0x01, 0x01, 0x01})
	exportBody := []byte{
		0x02,
		0x01, '0', 0x00, 0x00,
		0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x00,
	}
	out = appendCoreSection(out, 0x07, exportBody)
	funcBody := []byte{0x00}
	for i := range paramValtypes {
		funcBody = append(funcBody, 0x20)
		funcBody = leb128.UlebU64(funcBody, uint64(i))
	}
	funcBody = append(funcBody, 0x41, 0x00, 0x11, 0x00, 0x00, 0x0b)
	codeBody := []byte{0x01}
	codeBody = leb128.UlebU64(codeBody, uint64(len(funcBody)))
	codeBody = append(codeBody, funcBody...)
	out = appendCoreSection(out, 0x0a, codeBody)
	return out
}

// FixupModuleForParamsResults is FixupModuleForParamsNoResult with a non-empty
// result vector — the imported func type matches the results-carrying
// trampoline. resultValtypes empty falls back to the NoResult shape.
func FixupModuleForParamsResults(paramValtypes, resultValtypes []byte) []byte {
	if len(resultValtypes) == 0 {
		return FixupModuleForParamsNoResult(paramValtypes)
	}
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = leb128.UlebU64(typeBody, uint64(len(resultValtypes)))
	typeBody = append(typeBody, resultValtypes...)
	out = appendCoreSection(out, 0x01, typeBody)
	importBody := []byte{
		0x02,
		0x00, 0x01, '0', 0x00, 0x00,
		0x00, 0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x70, 0x01, 0x01, 0x01,
	}
	out = appendCoreSection(out, 0x02, importBody)
	elemBody := []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x00}
	out = appendCoreSection(out, 0x09, elemBody)
	return out
}

// repeatByte returns a slice of n copies of b.
func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// PutTypeSectionOneInstanceOneFuncNoResultExport emits a type
// section containing one instance type that inline-declares a
// no-result functype and exports it under exportName. Mirrors
// std/wasm/component's `put_type_section_one_instance_one_func_export`.
func PutTypeSectionOneInstanceOneFuncNoResultExport(buf []byte, exportName string, paramNames []string, paramValtypes []byte) []byte {
	return PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport(buf, nil, exportName, paramNames, paramValtypes)
}

// PutTypeSectionRawBody emits a type section whose body bytes are
// supplied directly by the caller. Escape hatch for instance
// types or defvaltype shapes that the structured composers can't
// express (resources, methods, type aliases inside an instance,
// etc.). The caller owns the full body — vec(N) count + N type
// entries — exactly as the binary wire format expects.
func PutTypeSectionRawBody(buf []byte, body []byte) []byte {
	return wrapSection(buf, SectionType, body)
}

// PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport
// generalises PutTypeSectionOneInstanceOneFuncNoResultExport by
// declaring N inner defvaltype entries inside the instance type
// before the functype. Param valtype bytes referencing inner
// typeidxs (i.e. byte 0x00..0x72) read as those inner-scope
// types after the binary parser; primitive valtype bytes
// (0x73..0x7f) stay primitive.
//
// Layout when len(innerTypes) > 0:
//
//	01                       // vec(1) type entries
//	42                       // instance-type form
//	<2+N>                    // vec(2+N) decls: N inner types + functype + export
//	(01 <inner-type-body>)*N // N type decls
//	01 40 ...                // functype decl (uses inner typeidxs)
//	04 00 <name> 01 <N>      // export decl referencing the functype at inner-typeidx N
//
// This is the shape wasm-tools emits for wasi:cli/exit's
// `func(status: result)`: one inner result type, one functype
// referencing it by inner-typeidx 0, and an export of the
// functype at inner-typeidx 1.
func PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport(buf []byte, innerTypes [][]byte, exportName string, paramNames []string, paramValtypes []byte) []byte {
	return PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf, innerTypes, exportName, paramNames, paramValtypes, nil)
}

// PutTypeSectionInstanceWithInnerTypesAndOneFuncExport is the
// result-aware generalisation. `resultValtypes` may be nil/empty
// (no result, "named-results vec(0)" wire form) or contain
// exactly one byte (single anonymous result of that valtype).
// Multi-result functions aren't supported yet — pass a typeidx
// reference for compound results via the inner-types channel
// instead.
func PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf []byte, innerTypes [][]byte, exportName string, paramNames []string, paramValtypes []byte, resultValtypes []byte) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	if len(resultValtypes) > 1 {
		panic("component: multi-result functions not yet supported")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)                         // vec(1) type entries
	body = append(body, 0x42)                              // instance-type form
	body = leb128.UlebU64(body, uint64(2+len(innerTypes))) // vec(2+N) decls

	// N inner type decls: each `01 <defvaltype-body>`.
	for _, it := range innerTypes {
		body = append(body, 0x01) // type decl
		body = append(body, it...)
	}

	// Functype decl at inner-typeidx N.
	body = append(body, 0x01) // type decl
	body = append(body, 0x40) // functype form
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	if len(resultValtypes) == 0 {
		body = append(body, 0x01)      // resultlist: named
		body = leb128.UlebU64(body, 0) // vec(0) results
	} else {
		body = append(body, 0x00)              // resultlist: single anonymous
		body = append(body, resultValtypes[0]) // valtype byte
	}

	// Export decl referencing the functype at inner-typeidx N.
	body = append(body, 0x04) // export decl
	body = append(body, 0x00) // exportname kind = label
	body = putName(body, exportName)
	body = append(body, 0x01)                            // externdesc kind = func
	body = leb128.UlebU64(body, uint64(len(innerTypes))) // typeidx = N (count of inner types)

	return wrapSection(buf, SectionType, body)
}

// InnerTypeResultEmpty is the defvaltype-body bytes for a
// `result<_, _>` (no payloads on either arm). Suitable as an entry
// in the `innerTypes` argument to
// PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport. The
// bytes are: 0x6a (result form), 0x00 (ok absent), 0x00 (err
// absent).
var InnerTypeResultEmpty = []byte{0x6a, 0x00, 0x00}

// InnerTypeBorrow returns the defvaltype body bytes for a
// `borrow<typeidx>` handle type. Used by resource-method
// signatures where the receiver (self) is a borrowed handle to
// a resource defined elsewhere in the same scope.
//
// Encoding:
//
//	68            -- borrow form (0x68)
//	<typeidx>     -- uleb: the resource typeidx the borrow refers to
func InnerTypeBorrow(resourceTypeidx uint32) []byte {
	out := []byte{0x68}
	out = leb128.UlebU64(out, uint64(resourceTypeidx))
	return out
}

// InnerTypeOwn returns the defvaltype body bytes for an `own<typeidx>` handle
// type (the owned counterpart of InnerTypeBorrow). Used by the P6 export lift
// for a handle-typed export parameter (docs/WIT-BRING-YOUR-OWN.md).
//
// Encoding: 69 <typeidx> (own form 0x69 + uleb resource typeidx).
func InnerTypeOwn(resourceTypeidx uint32) []byte {
	out := []byte{0x69}
	out = leb128.UlebU64(out, uint64(resourceTypeidx))
	return out
}

// InnerTypeListU8 is the defvaltype body bytes for `list<u8>` —
// the canonical-ABI byte-buffer shape used by wasi:io/streams's
// `blocking-write-and-flush(contents: list<u8>)` and similar.
//
// Encoding: `70 7d` (list form + u8 cvaltype).
var InnerTypeListU8 = []byte{0x70, CValtypeU8}

// InnerTypeList returns the defvaltype body for a `list<T>` where T is
// a primitive CValtype* or a small inner-scope typeidx. (InnerTypeListU8
// is the common `list<u8>` special case.)
//
// Encoding: 70 <elemValtype>
func InnerTypeList(elemValtype byte) []byte {
	return []byte{0x70, elemValtype}
}

// InnerTypeFuture returns the defvaltype body for a `future<elem>` — the WASI
// Preview-3 async single-value channel. Encoding `0x65 0x01 <elem>` (a payloadful
// future); a bare `future` (no payload) is `0x65 0x00`. Byte-verified against
// wasm-tools 1.240 (`future<u32>` → `65 01 79`). See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func InnerTypeFuture(elemValtype byte) []byte {
	return []byte{0x65, 0x01, elemValtype}
}

// InnerTypeStream returns the defvaltype body for a `stream<elem>` — the WASI
// Preview-3 async multi-value channel. Encoding `0x66 0x01 <elem>` (a payloadful
// stream); a bare `stream` is `0x66 0x00`. Byte-verified against wasm-tools 1.240
// (`stream<u8>` → `66 01 7d`). See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func InnerTypeStream(elemValtype byte) []byte {
	return []byte{0x66, 0x01, elemValtype}
}

// InnerTypeResultErr returns the defvaltype body for a
// `result<_, err=<typeidx>>` — an err-only result whose error
// arm carries the given typeidx. The ok arm is empty.
//
// Encoding:
//
//	6a            -- result form
//	00            -- ok absent
//	01 <typeidx>  -- err present + typeidx uleb
func InnerTypeResultErr(errTypeidx uint32) []byte {
	out := []byte{0x6a, 0x00, 0x01}
	out = leb128.UlebU64(out, uint64(errTypeidx))
	return out
}

// InnerTypeEnum returns the defvaltype body for an `enum` — a
// variant whose cases all lack payloads (just named discriminants).
//
// Encoding:
//
//	6d            -- enum form
//	<count>       -- uleb: number of cases
//	(<name>)*     -- each: uleb len + UTF-8 bytes
func InnerTypeEnum(names []string) []byte {
	out := []byte{0x6d}
	out = leb128.UlebU64(out, uint64(len(names)))
	for _, n := range names {
		out = putName(out, n)
	}
	return out
}

// InnerTypeFlags returns the defvaltype body for a `flags` type —
// a bitfield of named single-bit members.
//
// Encoding:
//
//	6e            -- flags form
//	<count>       -- uleb: number of members
//	(<name>)*     -- each: uleb len + UTF-8 bytes
func InnerTypeFlags(names []string) []byte {
	out := []byte{0x6e}
	out = leb128.UlebU64(out, uint64(len(names)))
	for _, n := range names {
		out = putName(out, n)
	}
	return out
}

// InnerTypeOption returns the defvaltype body for an `option<T>` —
// the canonical-ABI optional. innerValtype is a primitive CValtype*
// or a small inner-scope typeidx (the wrapped type).
//
// Encoding: 6b <valtype>
func InnerTypeOption(innerValtype byte) []byte {
	return []byte{0x6b, innerValtype}
}

// WasiFilesystemErrorCodeNames is the ordered case list of the
// `wasi:filesystem/types@0.2.0` `error-code` enum (37 cases). Order
// fixes the discriminant values, so it must match the WIT exactly
// (cmd/fern/wit/deps/filesystem/types.wit).
var WasiFilesystemErrorCodeNames = []string{
	"access", "would-block", "already", "bad-descriptor", "busy",
	"deadlock", "quota", "exist", "file-too-large", "illegal-byte-sequence",
	"in-progress", "interrupted", "invalid", "io", "is-directory", "loop",
	"too-many-links", "message-size", "name-too-long", "no-device",
	"no-entry", "no-lock", "insufficient-memory", "insufficient-space",
	"not-directory", "not-empty", "not-recoverable", "unsupported",
	"no-tty", "no-such-device", "overflow", "not-permitted", "pipe",
	"read-only", "invalid-seek", "text-file-busy", "cross-device",
}

// InnerTypeResultOk returns the defvaltype body for a `result<ok>`
// (ok arm typed, err arm empty) — wasi:http/types's
// `consume() -> result<incoming-body>` / `body() -> result<outgoing-body>`
// / `stream() -> result<input-stream>` / `write() -> result<output-stream>`.
//
// Encoding: 6a 01 <ok> 00
func InnerTypeResultOk(okTypeidx uint32) []byte {
	out := []byte{0x6a, 0x01}
	out = leb128.UlebU64(out, uint64(okTypeidx))
	return append(out, 0x00)
}

// InnerTypeResultOkErr returns the defvaltype body for a
// `result<ok=<okTypeidx>, err=<errTypeidx>>` — both arms typed.
// Used by wasi:io/streams::blocking-read, whose result is
// `result<list<u8>, stream-error>`.
//
// Encoding:
//
//	6a            -- result form
//	01 <ok>       -- ok present + typeidx uleb
//	01 <err>      -- err present + typeidx uleb
func InnerTypeResultOkErr(okTypeidx, errTypeidx uint32) []byte {
	out := []byte{0x6a, 0x01}
	out = leb128.UlebU64(out, uint64(okTypeidx))
	out = append(out, 0x01)
	out = leb128.UlebU64(out, uint64(errTypeidx))
	return out
}

// VariantCase describes one arm of a variant defvaltype.
// HasPayload selects whether the case carries a typed value.
// PayloadValtype is the valtype byte for that value (only
// meaningful when HasPayload is true; can be a primitive
// cvaltype constant or an inner-scope typeidx).
type VariantCase struct {
	Name           string
	HasPayload     bool
	PayloadValtype byte
}

// InnerTypeVariant returns the defvaltype body bytes for a
// `variant` with the given cases. Each case has a name, an
// optional typed payload, and (always) no "refines" annotation.
// "refines" semantics aren't needed by the preview-2 imports
// the fd_write arc targets — adding them is a future slice.
//
// Encoding:
//
//	71                       -- variant form (0x71)
//	<N>                      -- uleb: case count
//	(per case)
//	  <name>                 -- uleb len + bytes
//	  00 | 01 <valtype>      -- payload optional
//	  00                     -- no refines
func InnerTypeVariant(cases []VariantCase) []byte {
	out := []byte{0x71}
	out = leb128.UlebU64(out, uint64(len(cases)))
	for _, c := range cases {
		out = leb128.UlebU64(out, uint64(len(c.Name)))
		out = append(out, c.Name...)
		if c.HasPayload {
			out = append(out, 0x01, c.PayloadValtype)
		} else {
			out = append(out, 0x00)
		}
		out = append(out, 0x00) // no refines
	}
	return out
}

// InnerTypeTuple returns the defvaltype body for a `tuple<...>` —
// an ordered, unnamed product type. Each element is a single valtype
// byte (a primitive CValtype* or a small inner-scope typeidx, e.g.
// `ipv4-address = tuple<u8,u8,u8,u8>`).
//
// Encoding: 6f <count> <valtype>*
func InnerTypeTuple(elemValtypes []byte) []byte {
	out := []byte{0x6f}
	out = leb128.UlebU64(out, uint64(len(elemValtypes)))
	out = append(out, elemValtypes...)
	return out
}

// RecordField is one named field of an InnerTypeRecord. Valtype is a
// primitive CValtype* or a small inner-scope typeidx.
type RecordField struct {
	Name    string
	Valtype byte
}

// InnerTypeRecord returns the defvaltype body for a `record { ... }`
// — a named product type (e.g. `ipv4-socket-address = record { port:
// u16, address: ipv4-address }`).
//
// Encoding: 72 <count> (<name-len> <name> <valtype>)*
func InnerTypeRecord(fields []RecordField) []byte {
	out := []byte{0x72}
	out = leb128.UlebU64(out, uint64(len(fields)))
	for _, f := range fields {
		out = leb128.UlebU64(out, uint64(len(f.Name)))
		out = append(out, f.Name...)
		out = append(out, f.Valtype)
	}
	return out
}

// OuterAliasTypeDecl returns the bytes for an instance-type
// "alias outer" decl that exposes a parent-scope component type
// as an inner type in the enclosing instance type body. Used
// when an instance interface references a type declared by a
// sibling instance — e.g. wasi:io/streams's `error` resource
// references the resource declared inside wasi:io/error, after
// a top-level PutAliasSectionInstanceExportType has surfaced
// the cross-instance type.
//
// Inside-instance decl shape (5 bytes for small uleb values):
//
//	02          -- instance-decl marker: alias
//	03          -- sort = type
//	02          -- target form: outer
//	ct          -- uleb: scope count up (1 = parent component)
//	typeidx     -- uleb: typeidx in that outer scope
//
// Append these bytes to a RawInstanceTypeBody (between vec(N)
// count and N type entries) so the alias becomes part of the
// instance-type's decl list. The alias produces a NEW inner
// typeidx (the next slot in the instance's local type space),
// which the rest of the body can then reference as a primitive
// valtype byte (when < 0x73) or via a uleb-encoded typeidx in
// other decls.
func OuterAliasTypeDecl(scopesUp uint32, outerTypeidx uint32) []byte {
	out := []byte{0x02, 0x03, 0x02}
	out = leb128.UlebU64(out, uint64(scopesUp))
	out = leb128.UlebU64(out, uint64(outerTypeidx))
	return out
}

// ExportSubResourceDecl returns the bytes for an instance-type
// "export <name> (sub resource)" decl — the shape wasm-tools
// emits for resource declarations like `resource error;` inside
// wasi:io/error. Used to build RawInstanceTypeBody payloads for
// resource-bearing interfaces.
//
// Inside-instance decl shape:
//
//	04          -- instance-decl marker: export
//	00          -- exportname kind = label
//	<name>      -- uleb len + bytes
//	03          -- externdesc kind = type
//	01          -- typedef bound: sub (new resource type)
//
// For "export <name> (eq <typeidx>)" (alias an existing type
// under a name — see wasi:io/streams's `(export "error" (type
// (eq 1)))`), see ExportTypeEqDecl.
func ExportSubResourceDecl(name string) []byte {
	out := []byte{0x04, 0x00}
	out = leb128.UlebU64(out, uint64(len(name)))
	out = append(out, name...)
	out = append(out, 0x03, 0x01)
	return out
}

// ExportTypeEqDecl returns the bytes for an instance-type
// "export <name> (type (eq <typeidx>))" decl. Used to re-export
// an aliased outer type under a name, so that downstream
// references inside the same instance can refer to it by the
// alias's resulting inner typeidx.
//
// Inside-instance decl shape:
//
//	04          -- instance-decl marker: export
//	00          -- exportname kind = label
//	<name>      -- uleb len + bytes
//	03          -- externdesc kind = type
//	00          -- typedef bound: eq
//	<typeidx>   -- uleb (the typeidx the export equals)
func ExportTypeEqDecl(name string, typeidx uint32) []byte {
	out := []byte{0x04, 0x00}
	out = leb128.UlebU64(out, uint64(len(name)))
	out = append(out, name...)
	out = append(out, 0x03, 0x00)
	out = leb128.UlebU64(out, uint64(typeidx))
	return out
}

// WasiIoErrorInstanceTypeBody returns the type-section body bytes
// for the `wasi:io/error@0.2.0` instance type — the simplest
// preview-2 WASI import (just declares the `error` resource
// type). Suitable as a `WasiImport.RawInstanceTypeBody` value.
//
// The body is `vec(1) types + instance type form + vec(1) decls +
// ExportSubResourceDecl("error")`.
func WasiIoErrorInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x01}
	body = append(body, ExportSubResourceDecl("error")...)
	return body
}

// httpMethodCases / httpSchemeCases / httpHeaderErrorCases mirror the
// `variant method` / `variant scheme` / `variant header-error` of
// wasi:http/types@0.2.0 exactly — case order fixes the discriminant
// values, so it must match the WIT (cmd/fern/wit/deps/http/types.wit)
// for the produced component to link against the host's wasi:http.
var httpMethodCases = []VariantCase{
	{Name: "get"}, {Name: "head"}, {Name: "post"}, {Name: "put"},
	{Name: "delete"}, {Name: "connect"}, {Name: "options"},
	{Name: "trace"}, {Name: "patch"},
	{Name: "other", HasPayload: true, PayloadValtype: CValtypeString},
}

var httpSchemeCases = []VariantCase{
	{Name: "HTTP"}, {Name: "HTTPS"},
	{Name: "other", HasPayload: true, PayloadValtype: CValtypeString},
}

var httpHeaderErrorCases = []VariantCase{
	{Name: "invalid-syntax"}, {Name: "forbidden"}, {Name: "immutable"},
}

// httpValueTypeIdx holds the instance-type type indices of the named
// wasi:http/types value types after httpValueTypeDecls runs, so the
// resource-method func decls (the full http/types instance type, a
// later brick) can reference them.
type httpValueTypeIdx struct {
	method      uint32
	scheme      uint32
	headerError uint32
	errorCode   uint32
	optString   uint32 // option<string> — reused by path-with-query etc.
}

// httpErrorCodeCases builds the 39-case `variant error-code`. The
// payload-bearing arms reference the supplied type indices: the
// DNS-error / TLS-alert-received payload records, option<u64>,
// option<u32>, option<field-size-payload>, the bare field-size-payload
// record, and option<string>. All indices must be < 0x73 (single-byte
// valtype encoding); the http/types instance keeps well under that.
func httpErrorCodeCases(dnsPayload, tlsPayload, optU64, optU32, optFieldSize, fieldSize, optString uint32) []VariantCase {
	p := func(idx uint32) (bool, byte) { return true, byte(idx) }
	mk := func(name string, has bool, vt byte) VariantCase {
		return VariantCase{Name: name, HasPayload: has, PayloadValtype: vt}
	}
	dnsH, dnsV := p(dnsPayload)
	tlsH, tlsV := p(tlsPayload)
	u64H, u64V := p(optU64)
	u32H, u32V := p(optU32)
	ofsH, ofsV := p(optFieldSize)
	fsH, fsV := p(fieldSize)
	strH, strV := p(optString)
	return []VariantCase{
		mk("DNS-timeout", false, 0),
		mk("DNS-error", dnsH, dnsV),
		mk("destination-not-found", false, 0),
		mk("destination-unavailable", false, 0),
		mk("destination-IP-prohibited", false, 0),
		mk("destination-IP-unroutable", false, 0),
		mk("connection-refused", false, 0),
		mk("connection-terminated", false, 0),
		mk("connection-timeout", false, 0),
		mk("connection-read-timeout", false, 0),
		mk("connection-write-timeout", false, 0),
		mk("connection-limit-reached", false, 0),
		mk("TLS-protocol-error", false, 0),
		mk("TLS-certificate-error", false, 0),
		mk("TLS-alert-received", tlsH, tlsV),
		mk("HTTP-request-denied", false, 0),
		mk("HTTP-request-length-required", false, 0),
		mk("HTTP-request-body-size", u64H, u64V),
		mk("HTTP-request-method-invalid", false, 0),
		mk("HTTP-request-URI-invalid", false, 0),
		mk("HTTP-request-URI-too-long", false, 0),
		mk("HTTP-request-header-section-size", u32H, u32V),
		mk("HTTP-request-header-size", ofsH, ofsV),
		mk("HTTP-request-trailer-section-size", u32H, u32V),
		mk("HTTP-request-trailer-size", fsH, fsV),
		mk("HTTP-response-incomplete", false, 0),
		mk("HTTP-response-header-section-size", u32H, u32V),
		mk("HTTP-response-header-size", fsH, fsV),
		mk("HTTP-response-body-size", u64H, u64V),
		mk("HTTP-response-trailer-section-size", u32H, u32V),
		mk("HTTP-response-trailer-size", fsH, fsV),
		mk("HTTP-response-transfer-coding", strH, strV),
		mk("HTTP-response-content-coding", strH, strV),
		mk("HTTP-response-timeout", false, 0),
		mk("HTTP-upgrade-failed", false, 0),
		mk("HTTP-protocol-error", false, 0),
		mk("loop-detected", false, 0),
		mk("configuration-error", false, 0),
		mk("internal-error", strH, strV),
	}
}

// httpValueTypeDecls appends the wasi:http/types value-type decls
// (method / scheme / header-error / error-code + the supporting
// DNS-error-payload / TLS-alert-received-payload / field-size-payload
// records and the anonymous option<…> wrappers they reference) to a
// fresh decl stream starting at instance-type index `start`. It returns
// the decl bytes, the resulting named-type indices, and the next free
// index. Named records/variants are exported (referenced downstream by
// their export index, matching the sockets-network convention); the
// option<…> wrappers stay anonymous inner types.
func httpValueTypeDecls(start uint32) ([]byte, httpValueTypeIdx, uint32) {
	var decls []byte
	idx := start
	def := func(b []byte) uint32 {
		decls = append(decls, 0x01)
		decls = append(decls, b...)
		i := idx
		idx++
		return i
	}
	export := func(name string, typeidx uint32) uint32 {
		decls = append(decls, ExportTypeEqDecl(name, typeidx)...)
		i := idx
		idx++
		return i
	}

	optString := def(InnerTypeOption(CValtypeString))
	optU16 := def(InnerTypeOption(CValtypeU16))
	optU8 := def(InnerTypeOption(CValtypeU8))
	optU32 := def(InnerTypeOption(CValtypeU32))
	optU64 := def(InnerTypeOption(CValtypeU64))

	dnsDef := def(InnerTypeRecord([]RecordField{
		{Name: "rcode", Valtype: byte(optString)},
		{Name: "info-code", Valtype: byte(optU16)},
	}))
	dnsPayload := export("DNS-error-payload", dnsDef)
	tlsDef := def(InnerTypeRecord([]RecordField{
		{Name: "alert-id", Valtype: byte(optU8)},
		{Name: "alert-message", Valtype: byte(optString)},
	}))
	tlsPayload := export("TLS-alert-received-payload", tlsDef)
	fsDef := def(InnerTypeRecord([]RecordField{
		{Name: "field-name", Valtype: byte(optString)},
		{Name: "field-size", Valtype: byte(optU32)},
	}))
	fieldSize := export("field-size-payload", fsDef)
	optFieldSize := def(InnerTypeOption(byte(fieldSize)))

	methodDef := def(InnerTypeVariant(httpMethodCases))
	method := export("method", methodDef)
	schemeDef := def(InnerTypeVariant(httpSchemeCases))
	scheme := export("scheme", schemeDef)
	heDef := def(InnerTypeVariant(httpHeaderErrorCases))
	headerError := export("header-error", heDef)
	ecDef := def(InnerTypeVariant(httpErrorCodeCases(
		dnsPayload, tlsPayload, optU64, optU32, optFieldSize, fieldSize, optString)))
	errorCode := export("error-code", ecDef)

	return decls, httpValueTypeIdx{
		method:      method,
		scheme:      scheme,
		headerError: headerError,
		errorCode:   errorCode,
		optString:   optString,
	}, idx
}

// WasiHttpValueTypesInstanceTypeBody wraps httpValueTypeDecls in a
// standalone instance type exporting the named wasi:http/types value
// types — for validating the encoders independently before they're
// folded into the full http/types instance type.
func WasiHttpValueTypesInstanceTypeBody() []byte {
	decls, _, next := httpValueTypeDecls(0)
	body := []byte{0x01, 0x42}
	body = leb128.UlebU64(body, uint64(next)) // decl count == indices consumed from 0
	return append(body, decls...)
}

// WasiHttpTypesInstanceTypeBody returns the type-section body for the
// `wasi:http/types@0.2.0` instance type — the surface a
// wasi:http/incoming-handler core module imports. It declares the
// seven resources the handler touches (fields, incoming-request,
// incoming-body, future-trailers, outgoing-response, outgoing-body,
// response-outparam), the brick-1 value types (method / error-code /
// option<string> / …), and the fifteen method / constructor / static
// func decls + exports the core calls. The `[resource-drop]` imports
// the core also has are NOT declared here — those are the canon
// resource.drop intrinsic, lowered by the composer, not interface
// exports.
//
// input-stream / output-stream are outer-aliased from io/streams
// (incoming-body.stream / outgoing-body.write hand back streams); the
// caller surfaces them at the top level and passes their type indices.
//
// An imported instance type may be a subtype of the host's interface,
// so only the used methods are declared (the same way the tcp body
// declares just the listen/accept set).
func WasiHttpTypesInstanceTypeBody(inputStreamT, outputStreamT uint32) []byte {
	var decls []byte
	idx := uint32(0)
	declCount := uint32(0)

	// def appends a type-def decl (0x01 + body); returns its type index.
	def := func(b []byte) uint32 {
		decls = append(decls, 0x01)
		decls = append(decls, b...)
		declCount++
		i := idx
		idx++
		return i
	}
	aliasOuter := func(outerIdx uint32) uint32 {
		decls = append(decls, OuterAliasTypeDecl(1, outerIdx)...)
		declCount++
		i := idx
		idx++
		return i
	}
	subResource := func(name string) uint32 {
		decls = append(decls, ExportSubResourceDecl(name)...)
		declCount++
		i := idx
		idx++
		return i
	}
	// funcDef appends a functype decl; hasResult selects single
	// anonymous result (00 <idx>) vs no result (01 00). Returns the
	// functype's type index.
	funcDef := func(paramNames []string, paramVT []byte, hasResult bool, resultIdx byte) uint32 {
		decls = append(decls, 0x01, 0x40)
		decls = leb128.UlebU64(decls, uint64(len(paramNames)))
		for i, n := range paramNames {
			decls = leb128.UlebU64(decls, uint64(len(n)))
			decls = append(decls, n...)
			decls = append(decls, paramVT[i])
		}
		if hasResult {
			decls = append(decls, 0x00, resultIdx)
		} else {
			decls = append(decls, 0x01, 0x00)
		}
		declCount++
		i := idx
		idx++
		return i
	}
	// exportFunc appends a func export decl; does NOT consume a type idx.
	exportFunc := func(name string, funcIdx uint32) {
		decls = append(decls, 0x04, 0x00)
		decls = leb128.UlebU64(decls, uint64(len(name)))
		decls = append(decls, name...)
		decls = append(decls, 0x01)
		decls = leb128.UlebU64(decls, uint64(funcIdx))
		declCount++
	}

	// 0,1: outer aliases for the streams handed back by body methods.
	inStream := aliasOuter(inputStreamT)
	outStream := aliasOuter(outputStreamT)

	// Value types (method / scheme / header-error / error-code +
	// supporting records/options), continuing the index space.
	vtDecls, vt, next := httpValueTypeDecls(idx)
	decls = append(decls, vtDecls...)
	declCount += next - idx
	idx = next

	// Resources (7).
	fields := subResource("fields")
	incomingRequest := subResource("incoming-request")
	incomingBody := subResource("incoming-body")
	futureTrailers := subResource("future-trailers")
	outgoingResponse := subResource("outgoing-response")
	outgoingBody := subResource("outgoing-body")
	responseOutparam := subResource("response-outparam")

	// Borrows (method `self`).
	bIncReq := def(InnerTypeBorrow(incomingRequest))
	bFields := def(InnerTypeBorrow(fields))
	bIncBody := def(InnerTypeBorrow(incomingBody))
	bOutResp := def(InnerTypeBorrow(outgoingResponse))
	bOutBody := def(InnerTypeBorrow(outgoingBody))

	// Owns (constructor / static `this` / handle returns).
	oFields := def([]byte{0x69, byte(fields)})
	oIncBody := def([]byte{0x69, byte(incomingBody)})
	oFutTrail := def([]byte{0x69, byte(futureTrailers)})
	oInStream := def([]byte{0x69, byte(inStream)})
	oOutStream := def([]byte{0x69, byte(outStream)})
	oOutResp := def([]byte{0x69, byte(outgoingResponse)})
	oOutBody := def([]byte{0x69, byte(outgoingBody)})
	oOutparam := def([]byte{0x69, byte(responseOutparam)})

	// Result wrappers.
	rIncBody := def(InnerTypeResultOk(oIncBody))     // consume
	rInStream := def(InnerTypeResultOk(oInStream))   // incoming-body.stream
	rOutBody := def(InnerTypeResultOk(oOutBody))     // outgoing-response.body
	rOutStream := def(InnerTypeResultOk(oOutStream)) // outgoing-body.write
	rHeaderErr := def(InnerTypeResultErr(vt.headerError))
	rEmpty := def(InnerTypeResultEmpty)
	rErrCode := def(InnerTypeResultErr(vt.errorCode)) // outgoing-body.finish
	rOutRespErr := def(InnerTypeResultOkErr(oOutResp, vt.errorCode))

	// fields.entries result: list<tuple<field-key=string, field-value=list<u8>>>.
	fieldValue := def(InnerTypeListU8)
	entryTuple := def(InnerTypeTuple([]byte{CValtypeString, byte(fieldValue)}))
	entriesList := def(InnerTypeList(byte(entryTuple)))

	// option<trailers> = option<own<fields>> (outgoing-body.finish).
	optTrailers := def(InnerTypeOption(byte(oFields)))

	// --- Method / constructor / static func decls + exports. ---
	f := funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(vt.method))
	exportFunc("[method]incoming-request.method", f)
	f = funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(vt.optString))
	exportFunc("[method]incoming-request.path-with-query", f)
	f = funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(oFields))
	exportFunc("[method]incoming-request.headers", f)
	f = funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(rIncBody))
	exportFunc("[method]incoming-request.consume", f)

	f = funcDef([]string{"self"}, []byte{byte(bIncBody)}, true, byte(rInStream))
	exportFunc("[method]incoming-body.stream", f)
	f = funcDef([]string{"this"}, []byte{byte(oIncBody)}, true, byte(oFutTrail))
	exportFunc("[static]incoming-body.finish", f)

	f = funcDef(nil, nil, true, byte(oFields))
	exportFunc("[constructor]fields", f)
	f = funcDef([]string{"self"}, []byte{byte(bFields)}, true, byte(entriesList))
	exportFunc("[method]fields.entries", f)
	f = funcDef([]string{"self", "name", "value"},
		[]byte{byte(bFields), CValtypeString, byte(fieldValue)}, true, byte(rHeaderErr))
	exportFunc("[method]fields.append", f)

	f = funcDef([]string{"headers"}, []byte{byte(oFields)}, true, byte(oOutResp))
	exportFunc("[constructor]outgoing-response", f)
	f = funcDef([]string{"self", "status-code"}, []byte{byte(bOutResp), CValtypeU16}, true, byte(rEmpty))
	exportFunc("[method]outgoing-response.set-status-code", f)
	f = funcDef([]string{"self"}, []byte{byte(bOutResp)}, true, byte(rOutBody))
	exportFunc("[method]outgoing-response.body", f)

	f = funcDef([]string{"self"}, []byte{byte(bOutBody)}, true, byte(rOutStream))
	exportFunc("[method]outgoing-body.write", f)
	f = funcDef([]string{"this", "trailers"}, []byte{byte(oOutBody), byte(optTrailers)}, true, byte(rErrCode))
	exportFunc("[static]outgoing-body.finish", f)

	f = funcDef([]string{"param", "response"}, []byte{byte(oOutparam), byte(rOutRespErr)}, false, 0)
	exportFunc("[static]response-outparam.set", f)

	body := []byte{0x01, 0x42}
	body = leb128.UlebU64(body, uint64(declCount))
	return append(body, decls...)
}

// WasiIoPollInstanceTypeBody returns the type-section body for a
// minimal `wasi:io/poll@0.2.0` instance type: the `pollable` resource
// plus its `block: func()` method. TCP's tcp-socket.subscribe returns
// an own<pollable>, and a socket server blocks on it; the resource is
// also dropped via canon resource.drop.
//
// Decls: 0 pollable (sub-resource), 1 borrow<pollable>, 2 func(self)
// -> (), 3 export "[method]pollable.block" (func 0).
// WasiIoPollInstanceTypeBody builds the wasi:io/poll instance type.
// When withPoll is false it is the minimal block-only shape the socket
// shapes use (pollable + pollable.block); when true it additionally
// declares the readiness multiplexer `poll(in: list<pollable>) ->
// list<u32>` — the wasm reactor's fan-out primitive. The socket path
// passes false so its imported instance is byte-identical to before;
// only the reactor (req.Poll) opts into the heavier shape.
func WasiIoPollInstanceTypeBody(withPoll bool) []byte {
	declCount := byte(4)
	if withPoll {
		declCount = 8
	}
	body := []byte{0x01, 0x42, declCount}
	body = append(body, ExportSubResourceDecl("pollable")...) // 0
	body = append(body, 0x01, 0x68, 0x00)                     // 1: borrow<pollable=0>
	// 2: func(self: borrow 1) -> () (no result)
	body = append(body,
		0x01, 0x40, 0x01,
		0x04, 's', 'e', 'l', 'f', 0x01,
		0x01, 0x00,
	)
	// 3: export "[method]pollable.block" (functype is decl/type 2)
	body = append(body, 0x04, 0x00, byte(len("[method]pollable.block")))
	body = append(body, "[method]pollable.block"...)
	body = append(body, 0x01, 0x02)
	if withPoll {
		// Func exports do NOT consume a type index, so after the block
		// functype (type 2) the next types are 3, 4, 5:
		//   type 3: list<borrow<pollable>=1>
		body = append(body, 0x01, 0x70, 0x01)
		//   type 4: list<u32>
		body = append(body, 0x01, 0x70, CValtypeU32)
		//   type 5: func(in: list 3) -> list 4
		body = append(body, tcpMethodFuncDecl("poll", []string{"in"}, []byte{0x03}, 0x04)...)
		// export "poll" (functype is type 5)
		body = append(body, 0x04, 0x00, byte(len("poll")))
		body = append(body, "poll"...)
		body = append(body, 0x01, 0x05)
	}
	return body
}

// WasiClocksMonotonicTimerInstanceTypeBody returns the type-section
// body for a minimal `wasi:clocks/monotonic-clock@0.2.0` instance
// type exposing only `subscribe-duration(when: duration) -> pollable`
// — the wasm reactor's timer primitive. `duration` is u64 nanoseconds;
// the returned `pollable` is the wasi:io/poll `pollable` resource,
// outer-aliased from the surfaced top-level type (the caller surfaces
// it via ensureIoPoll) so the resource identity matches io/poll's
// block/poll methods — exactly how tcp-socket.subscribe's own<pollable>
// lines up with pollable.block, but here with no socket in play.
//
// Decls: 0 outer-alias pollable, 1 own<pollable=0>, 2 func(when: u64)
// -> own<pollable=1>, 3 export "subscribe-duration" (func 2). 4 decls.
func WasiClocksMonotonicTimerInstanceTypeBody(pollableT uint32) []byte {
	body := []byte{0x01, 0x42, 0x04}                         // 4 decls
	body = append(body, OuterAliasTypeDecl(1, pollableT)...) // 0: pollable
	body = append(body, 0x01, 0x69, 0x00)                    // 1: own<pollable=0>
	// 2: func(when: u64) -> own<pollable=1>
	body = append(body, tcpMethodFuncDecl("subscribe-duration",
		[]string{"when"}, []byte{CValtypeU64}, 0x01)...)
	// 3: export "subscribe-duration" (functype is decl/type 2)
	const name = "subscribe-duration"
	body = append(body, 0x04, 0x00, byte(len(name)))
	body = append(body, name...)
	body = append(body, 0x01, 0x02)
	return body
}

// WasiClocksMonotonicTimerAndNowInstanceTypeBody is the combined
// monotonic-clock import instance type, exporting BOTH `subscribe-duration`
// (the timer pollable, for wasm_timer_pollable) AND `now` (an instant=u64, for
// monotonic_ns). A program that uses both — e.g. std/async's with_deadline,
// which tracks elapsed time with `now` AND arms a deadline pollable with
// `subscribe-duration` — needs one instance exporting both, since a component
// can only import a given interface once. (Using either alone keeps the
// single-export timer body above / the structured `now` type.)
func WasiClocksMonotonicTimerAndNowInstanceTypeBody(pollableT uint32) []byte {
	body := []byte{0x01, 0x42, 0x06}                         // 6 decls
	body = append(body, OuterAliasTypeDecl(1, pollableT)...) // 0: pollable (typeidx 0)
	body = append(body, 0x01, 0x69, 0x00)                    // 1: own<pollable=0> (typeidx 1)
	// 2: func(when: u64) -> own<pollable=1>  (typeidx 2)
	body = append(body, tcpMethodFuncDecl("subscribe-duration",
		[]string{"when"}, []byte{CValtypeU64}, 0x01)...)
	// 3: export "subscribe-duration" (functype is typeidx 2)
	const sd = "subscribe-duration"
	body = append(body, 0x04, 0x00, byte(len(sd)))
	body = append(body, sd...)
	body = append(body, 0x01, 0x02)
	// 4: type func() -> u64  (typeidx 3) — `now` returns instant (= u64)
	body = append(body, 0x01, 0x40, 0x00, 0x00, CValtypeU64)
	// 5: export "now" (functype is typeidx 3)
	body = append(body, 0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x03)
	return body
}

// WasiSocketsNetworkErrorCodeNames is the ordered case list of the
// `wasi:sockets/network@0.2.0` error-code enum (21 cases). Order
// fixes the discriminant values, so it must match the WIT exactly
// for the produced component to link against wasmtime's host sockets.
var WasiSocketsNetworkErrorCodeNames = []string{
	"unknown", "access-denied", "not-supported", "invalid-argument",
	"out-of-memory", "timeout", "concurrency-conflict", "not-in-progress",
	"would-block", "invalid-state", "new-socket-limit",
	"address-not-bindable", "address-in-use", "remote-unreachable",
	"connection-refused", "connection-reset", "connection-aborted",
	"datagram-too-large", "name-unresolvable", "temporary-resolver-failure",
	"permanent-resolver-failure",
}

// WasiSocketsNetworkInstanceTypeBody returns the type-section body
// for the `wasi:sockets/network@0.2.0` instance type — the
// type-heavy core every socket interface references: the `network`
// resource, the `error-code` enum, `ip-address-family`, and the full
// `ip-socket-address` variant (ipv4 / ipv6 socket-address records
// over ipv4-address tuple<u8×4> / ipv6-address tuple<u16×8>). Exports
// network / error-code / ip-address-family / ip-socket-address for
// tcp-create-socket and tcp to outer-alias.
//
// Every named type is exported, and exported types reference only
// other exported types — wasm-tools rejects an imported instance
// whose exported type reaches a non-exported one. This matches the
// real WIT (ipv4-address / ipv6-address / the socket-address records
// are all named exports).
//
// Type indices: 0 network, 1 error-code enum / 2 export, 3
// ip-address-family enum / 4 export, 5 ipv4-address tuple / 6 export,
// 7 ipv4-socket-address record / 8 export, 9 ipv6-address tuple / 10
// export, 11 ipv6-socket-address record / 12 export, 13
// ip-socket-address variant / 14 export. (15 decls.)
func WasiSocketsNetworkInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x0f}                         // 15 decls
	body = append(body, ExportSubResourceDecl("network")...) // 0
	body = append(body, 0x01)                                // 1: error-code enum
	body = append(body, InnerTypeEnum(WasiSocketsNetworkErrorCodeNames)...)
	body = append(body, ExportTypeEqDecl("error-code", 1)...) // 2
	body = append(body, 0x01)                                 // 3: ip-address-family enum
	body = append(body, InnerTypeEnum([]string{"ipv4", "ipv6"})...)
	body = append(body, ExportTypeEqDecl("ip-address-family", 3)...) // 4
	body = append(body, 0x01)                                        // 5: ipv4-address tuple<u8×4>
	body = append(body, InnerTypeTuple([]byte{CValtypeU8, CValtypeU8, CValtypeU8, CValtypeU8})...)
	body = append(body, ExportTypeEqDecl("ipv4-address", 5)...) // 6
	body = append(body, 0x01)                                   // 7: ipv4-socket-address record (address → exported 6)
	body = append(body, InnerTypeRecord([]RecordField{
		{Name: "port", Valtype: CValtypeU16},
		{Name: "address", Valtype: 0x06},
	})...)
	body = append(body, ExportTypeEqDecl("ipv4-socket-address", 7)...) // 8
	body = append(body, 0x01)                                          // 9: ipv6-address tuple<u16×8>
	body = append(body, InnerTypeTuple([]byte{
		CValtypeU16, CValtypeU16, CValtypeU16, CValtypeU16,
		CValtypeU16, CValtypeU16, CValtypeU16, CValtypeU16,
	})...)
	body = append(body, ExportTypeEqDecl("ipv6-address", 9)...) // 10
	body = append(body, 0x01)                                   // 11: ipv6-socket-address record (address → exported 10)
	body = append(body, InnerTypeRecord([]RecordField{
		{Name: "port", Valtype: CValtypeU16},
		{Name: "flow-info", Valtype: CValtypeU32},
		{Name: "address", Valtype: 0x0a},
		{Name: "scope-id", Valtype: CValtypeU32},
	})...)
	body = append(body, ExportTypeEqDecl("ipv6-socket-address", 11)...) // 12
	body = append(body, 0x01)                                           // 13: ip-socket-address variant (cases → exported 8, 12)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "ipv4", HasPayload: true, PayloadValtype: 0x08},
		{Name: "ipv6", HasPayload: true, PayloadValtype: 0x0c},
	})...)
	body = append(body, ExportTypeEqDecl("ip-socket-address", 13)...) // 14
	return body
}

// WasiSocketsInstanceNetworkInstanceTypeBody returns the type-section
// body for `wasi:sockets/instance-network@0.2.0`:
// `instance-network: func() -> own<network>`. The `network` resource
// is outer-aliased from the wasi:sockets/network import (surfaced at
// the top level via PutAliasSectionInstanceExportType, then referenced
// here with OuterAliasTypeDecl) — the same cross-instance pattern
// io/streams uses for io/error's `error`. outerNetworkTypeidx is that
// top-level type index.
//
// Decls: 0 alias-outer network, 1 own<network>, 2 func() ->
// own<network>, 3 export "instance-network" (func 2).
func WasiSocketsInstanceNetworkInstanceTypeBody(outerNetworkTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x04}                                   // 4 decls
	body = append(body, OuterAliasTypeDecl(1, outerNetworkTypeidx)...) // 0
	body = append(body, 0x01, 0x69, 0x00)                              // 1: own<network=0>
	// 2: func() -> own<network=1> (no params, single anonymous result)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x01)
	// 3: export "instance-network" (func is type 2)
	body = append(body, 0x04, 0x00, byte(len("instance-network")))
	body = append(body, "instance-network"...)
	body = append(body, 0x01, 0x02)
	return body
}

// tcpMethodFuncDecl builds an instance-type func decl + its export.
// params is a flat list of (name, valtype) pairs; resultTypeidx is the
// single anonymous result. Returns the bytes for `0x01 0x40 ... +
// export`.
func tcpMethodFuncDecl(method string, paramNames []string, paramValtypes []byte, resultTypeidx byte) []byte {
	out := []byte{0x01, 0x40}
	out = leb128.UlebU64(out, uint64(len(paramNames)))
	for i, n := range paramNames {
		out = leb128.UlebU64(out, uint64(len(n)))
		out = append(out, n...)
		out = append(out, paramValtypes[i])
	}
	out = append(out, 0x00, resultTypeidx) // single anonymous result
	return out
}

// WasiSocketsTcpInstanceTypeBody returns the type-section body for
// `wasi:sockets/tcp@0.2.0` — the `tcp-socket` resource plus the six
// methods a listening server uses: start-bind / finish-bind /
// start-listen / finish-listen / accept / subscribe. It outer-aliases
// network / error-code / ip-socket-address (from sockets/network),
// input-stream / output-stream (from io/streams), and pollable (from
// io/poll); the caller must have surfaced those six at the top level
// (PutAliasSectionInstanceExportType) and passes their type indices.
//
// accept returns result<tuple<own<tcp-socket>, own<input-stream>,
// own<output-stream>>, error-code>; subscribe returns own<pollable>;
// the bind/listen methods return result<_, error-code>.
//
// Inner type indices: 0-5 the six outer aliases, 6 tcp-socket
// resource, 7 borrow<tcp-socket>, 8 borrow<network>, 9
// result<_,error-code>, 10-13 own<tcp-socket|input|output|pollable>,
// 14 tuple<10,11,12>, 15 result<14,error-code>, then 16/18/20/22/24/26
// the method functypes (each followed by its export). 28 decls.
func WasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT uint32) []byte {
	return wasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT, false)
}

// WasiSocketsTcpConnectInstanceTypeBody is the outbound-client variant:
// the same tcp-socket instance type plus the connect chain
// (start-connect / finish-connect). finish-connect returns
// result<tuple<own input-stream, own output-stream>, error-code>. The
// server path uses the (smaller) bind/listen/accept shape via
// WasiSocketsTcpInstanceTypeBody; the connect methods are a strict
// superset appended after subscribe, so the type indices the server
// path relies on (0-21) are unchanged.
func WasiSocketsTcpConnectInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT uint32) []byte {
	return wasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT, true)
}

func wasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT uint32, withConnect bool) []byte {
	exportMethod := func(body []byte, name string, funcTypeidx byte) []byte {
		body = append(body, 0x04, 0x00, byte(len(name)))
		body = append(body, name...)
		return append(body, 0x01, funcTypeidx)
	}
	declCount := byte(0x1c) // 28
	if withConnect {
		declCount = 0x22 // 34: +tuple, +result, +2 functypes, +2 exports
	}
	body := []byte{0x01, 0x42, declCount}
	body = append(body, OuterAliasTypeDecl(1, networkT)...)      // 0
	body = append(body, OuterAliasTypeDecl(1, errorCodeT)...)    // 1
	body = append(body, OuterAliasTypeDecl(1, ipSockAddrT)...)   // 2
	body = append(body, OuterAliasTypeDecl(1, inputStreamT)...)  // 3
	body = append(body, OuterAliasTypeDecl(1, outputStreamT)...) // 4
	body = append(body, OuterAliasTypeDecl(1, pollableT)...)     // 5
	body = append(body, ExportSubResourceDecl("tcp-socket")...)  // 6
	body = append(body, 0x01, 0x68, 0x06)                        // 7: borrow<tcp-socket>
	body = append(body, 0x01, 0x68, 0x00)                        // 8: borrow<network>
	body = append(body, 0x01)                                    // 9: result<_, error-code=1>
	body = append(body, InnerTypeResultErr(1)...)
	body = append(body, 0x01, 0x69, 0x06) // 10: own<tcp-socket>
	body = append(body, 0x01, 0x69, 0x03) // 11: own<input-stream>
	body = append(body, 0x01, 0x69, 0x04) // 12: own<output-stream>
	body = append(body, 0x01, 0x69, 0x05) // 13: own<pollable>
	body = append(body, 0x01)             // 14: tuple<own tcp-socket, own input, own output>
	body = append(body, InnerTypeTuple([]byte{0x0a, 0x0b, 0x0c})...)
	body = append(body, 0x01) // 15: result<tuple=14, error-code=1>
	body = append(body, InnerTypeResultOkErr(14, 1)...)
	// 16: start-bind(self: borrow 7, network: borrow 8, local-address: ip-sock-addr 2) -> result 9
	body = append(body, tcpMethodFuncDecl("start-bind",
		[]string{"self", "network", "local-address"}, []byte{0x07, 0x08, 0x02}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.start-bind", 0x10)
	// 17: finish-bind(self) -> result 9  (func exports don't consume a type index)
	body = append(body, tcpMethodFuncDecl("finish-bind", []string{"self"}, []byte{0x07}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.finish-bind", 0x11)
	// 18: start-listen(self) -> result 9
	body = append(body, tcpMethodFuncDecl("start-listen", []string{"self"}, []byte{0x07}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.start-listen", 0x12)
	// 19: finish-listen(self) -> result 9
	body = append(body, tcpMethodFuncDecl("finish-listen", []string{"self"}, []byte{0x07}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.finish-listen", 0x13)
	// 20: accept(self) -> result 15
	body = append(body, tcpMethodFuncDecl("accept", []string{"self"}, []byte{0x07}, 0x0f)...)
	body = exportMethod(body, "[method]tcp-socket.accept", 0x14)
	// 21: subscribe(self) -> own<pollable> 13
	body = append(body, tcpMethodFuncDecl("subscribe", []string{"self"}, []byte{0x07}, 0x0d)...)
	body = exportMethod(body, "[method]tcp-socket.subscribe", 0x15)
	if withConnect {
		// 22: tuple<own input-stream=11, own output-stream=12>
		body = append(body, 0x01)
		body = append(body, InnerTypeTuple([]byte{0x0b, 0x0c})...)
		// 23: result<tuple=22, error-code=1>
		body = append(body, 0x01)
		body = append(body, InnerTypeResultOkErr(22, 1)...)
		// 24: start-connect(self 7, network 8, remote-address 2) -> result 9
		body = append(body, tcpMethodFuncDecl("start-connect",
			[]string{"self", "network", "remote-address"}, []byte{0x07, 0x08, 0x02}, 0x09)...)
		body = exportMethod(body, "[method]tcp-socket.start-connect", 0x18)
		// 25: finish-connect(self 7) -> result 23
		body = append(body, tcpMethodFuncDecl("finish-connect", []string{"self"}, []byte{0x07}, 0x17)...)
		body = exportMethod(body, "[method]tcp-socket.finish-connect", 0x19)
	}
	return body
}

// WasiSocketsTcpCreateSocketInstanceTypeBody returns the type-section
// body for `wasi:sockets/tcp-create-socket@0.2.0`:
// `create-tcp-socket(address-family: ip-address-family) ->
// result<own<tcp-socket>, error-code>`. It outer-aliases
// ip-address-family + error-code (from sockets/network) and
// tcp-socket (from sockets/tcp); the caller surfaces those three at
// the top level and passes their type indices.
//
// Decls: 0 alias ip-address-family, 1 alias error-code, 2 alias
// tcp-socket, 3 own<tcp-socket>, 4 result<own,error-code>, 5 func, 6
// export "create-tcp-socket".
func WasiSocketsTcpCreateSocketInstanceTypeBody(ipAddrFamilyT, errorCodeT, tcpSocketT uint32) []byte {
	body := []byte{0x01, 0x42, 0x07}                             // 7 decls
	body = append(body, OuterAliasTypeDecl(1, ipAddrFamilyT)...) // 0
	body = append(body, OuterAliasTypeDecl(1, errorCodeT)...)    // 1
	body = append(body, OuterAliasTypeDecl(1, tcpSocketT)...)    // 2
	body = append(body, 0x01, 0x69, 0x02)                        // 3: own<tcp-socket=2>
	body = append(body, 0x01)                                    // 4: result<own=3, error-code=1>
	body = append(body, InnerTypeResultOkErr(3, 1)...)
	// 5: create-tcp-socket(address-family: ip-address-family=0) -> result 4
	body = append(body, tcpMethodFuncDecl("create-tcp-socket", []string{"address-family"}, []byte{0x00}, 0x04)...)
	// 6: export "create-tcp-socket" (func is type 5)
	body = append(body, 0x04, 0x00, byte(len("create-tcp-socket")))
	body = append(body, "create-tcp-socket"...)
	body = append(body, 0x01, 0x05)
	return body
}

// WasiSocketsUdpInstanceTypeBody returns the type-section body for the
// send-only subset of `wasi:sockets/udp@0.2.0` — what udp_send (one-shot
// fire-and-forget datagram) needs: the udp-socket resource plus
// start-bind / finish-bind / stream, and the outgoing-datagram-stream
// resource plus check-send / send. The incoming-datagram-stream is
// declared as a bare resource (stream() returns a tuple of both, and
// the unused incoming half is dropped) but carries no methods — instance
// subtyping lets a send-only client omit receive / subscribe.
//
// Unlike TCP, the datagram path is NOT wasi:io/streams: send takes a
// `list<outgoing-datagram>` (each `{ data: list<u8>, remote-address:
// option<ip-socket-address> }`). outgoing-datagram-stream also exposes
// subscribe -> own<pollable>, so a sender can block until the stream
// permits a datagram (wasmtime >=45 rejects a send that exceeds the
// last check-send permit). It outer-aliases network / error-code /
// ip-socket-address from sockets/network plus pollable from io/poll;
// the caller surfaces those four and passes their type indices.
func WasiSocketsUdpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, pollableT uint32) []byte {
	var decls []byte
	idx := uint32(0)
	declCount := uint32(0)
	alias := func(outer uint32) uint32 {
		decls = append(decls, OuterAliasTypeDecl(1, outer)...)
		declCount++
		i := idx
		idx++
		return i
	}
	sub := func(name string) uint32 {
		decls = append(decls, ExportSubResourceDecl(name)...)
		declCount++
		i := idx
		idx++
		return i
	}
	def := func(b []byte) uint32 {
		decls = append(decls, 0x01)
		decls = append(decls, b...)
		declCount++
		i := idx
		idx++
		return i
	}
	exportType := func(name string, typeidx uint32) uint32 {
		decls = append(decls, ExportTypeEqDecl(name, typeidx)...)
		declCount++
		i := idx
		idx++
		return i
	}
	method := func(name string, paramNames []string, paramVT []byte, resultIdx byte) {
		decls = append(decls, tcpMethodFuncDecl(name, paramNames, paramVT, resultIdx)...)
		declCount++
		idx++ // the functype decl
		decls = append(decls, 0x04, 0x00, byte(len(name)))
		decls = append(decls, name...)
		decls = append(decls, 0x01, byte(idx-1)) // export refs the functype just emitted
		declCount++
	}

	netT := alias(networkT)
	errT := alias(errorCodeT)
	// Re-export ip-socket-address under its name: the exported
	// outgoing-datagram record references it (transitively), and an
	// exported type may only reach other exported named types.
	sockAddrT := exportType("ip-socket-address", alias(ipSockAddrT))
	pollT := alias(pollableT)

	udpSock := sub("udp-socket")
	inStream := sub("incoming-datagram-stream")
	outStream := sub("outgoing-datagram-stream")

	listU8 := def(InnerTypeListU8)
	optAddr := def(InnerTypeOption(byte(sockAddrT)))
	// outgoing-datagram is a named record in the WIT; export it so the
	// send method's list<outgoing-datagram> param reaches an exported
	// named type.
	outDatagram := exportType("outgoing-datagram", def(InnerTypeRecord([]RecordField{
		{Name: "data", Valtype: byte(listU8)},
		{Name: "remote-address", Valtype: byte(optAddr)},
	})))
	bUdp := def(InnerTypeBorrow(udpSock))
	bNet := def(InnerTypeBorrow(netT))
	bOut := def(InnerTypeBorrow(outStream))
	ownPoll := def([]byte{0x69, byte(pollT)})
	resEmptyErr := def(InnerTypeResultErr(errT))
	ownIn := def([]byte{0x69, byte(inStream)})
	ownOut := def([]byte{0x69, byte(outStream)})
	tupStreams := def(InnerTypeTuple([]byte{byte(ownIn), byte(ownOut)}))
	resStream := def(InnerTypeResultOkErr(tupStreams, errT))
	resU64 := def(InnerTypeResultOkErr(CValtypeU64, errT))
	listDatagram := def(InnerTypeList(byte(outDatagram)))

	method("[method]udp-socket.start-bind",
		[]string{"self", "network", "local-address"}, []byte{byte(bUdp), byte(bNet), byte(sockAddrT)}, byte(resEmptyErr))
	method("[method]udp-socket.finish-bind", []string{"self"}, []byte{byte(bUdp)}, byte(resEmptyErr))
	method("[method]udp-socket.stream", []string{"self", "remote-address"}, []byte{byte(bUdp), byte(optAddr)}, byte(resStream))
	method("[method]outgoing-datagram-stream.check-send", []string{"self"}, []byte{byte(bOut)}, byte(resU64))
	method("[method]outgoing-datagram-stream.send", []string{"self", "datagrams"}, []byte{byte(bOut), byte(listDatagram)}, byte(resU64))
	method("[method]outgoing-datagram-stream.subscribe", []string{"self"}, []byte{byte(bOut)}, byte(ownPoll))

	body := []byte{0x01, 0x42}
	body = leb128.UlebU64(body, uint64(declCount))
	return append(body, decls...)
}

// WasiSocketsUdpCreateSocketInstanceTypeBody returns the type-section
// body for `wasi:sockets/udp-create-socket@0.2.0`:
// `create-udp-socket(address-family: ip-address-family) ->
// result<own<udp-socket>, error-code>`. Mirrors the tcp-create-socket
// body — outer-aliases ip-address-family + error-code (sockets/network)
// and udp-socket (sockets/udp).
//
// Decls: 0 alias ip-address-family, 1 alias error-code, 2 alias
// udp-socket, 3 own<udp-socket>, 4 result<own,error-code>, 5 func, 6
// export "create-udp-socket".
func WasiSocketsUdpCreateSocketInstanceTypeBody(ipAddrFamilyT, errorCodeT, udpSocketT uint32) []byte {
	body := []byte{0x01, 0x42, 0x07}                             // 7 decls
	body = append(body, OuterAliasTypeDecl(1, ipAddrFamilyT)...) // 0
	body = append(body, OuterAliasTypeDecl(1, errorCodeT)...)    // 1
	body = append(body, OuterAliasTypeDecl(1, udpSocketT)...)    // 2
	body = append(body, 0x01, 0x69, 0x02)                        // 3: own<udp-socket=2>
	body = append(body, 0x01)                                    // 4: result<own=3, error-code=1>
	body = append(body, InnerTypeResultOkErr(3, 1)...)
	body = append(body, tcpMethodFuncDecl("create-udp-socket", []string{"address-family"}, []byte{0x00}, 0x04)...)
	body = append(body, 0x04, 0x00, byte(len("create-udp-socket")))
	body = append(body, "create-udp-socket"...)
	body = append(body, 0x01, 0x05)
	return body
}

// WasiFilesystemTypesDescriptorInstanceTypeBody returns the
// type-section body for a minimal `wasi:filesystem/types@0.2.0`
// instance type that declares just the `descriptor` resource —
// the handle returned by `wasi:filesystem/preopens::get-directories`
// and the receiver for the file open / read / write methods. Same
// shape as WasiIoErrorInstanceTypeBody (one exported sub-resource);
// it's the foundational brick of the path_* migration, with the
// descriptor methods and the preopens getter layered on in
// subsequent bricks (which reference this resource via an outer
// alias, the wasi:io/error → wasi:io/streams pattern).
func WasiFilesystemTypesDescriptorInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x01}
	body = append(body, ExportSubResourceDecl("descriptor")...)
	return body
}

// WasiFilesystemTypesReadViaStreamInstanceTypeBody returns the
// type-section body for a `wasi:filesystem/types@0.2.0` instance
// type declaring the `descriptor` resource, the `error-code` enum,
// and the `read-via-stream` method:
//
//	read-via-stream: func(self: borrow<descriptor>, offset: u64)
//	    -> result<own<input-stream>, error-code>
//
// `input-stream` lives in wasi:io/streams, pulled in via an outer
// alias of `outerInputStreamTypeidx`. This is the descriptor method
// the file-read path uses: open-at gives a descriptor, then
// read-via-stream turns it into an input-stream that blocking-read
// drains.
//
// Inside-instance decl list (10 decls):
//
//  0. export "descriptor" (sub resource)        → typeidx 0
//  1. alias outer 1 <outerInputStreamTypeidx>    → typeidx 1
//  2. export "input-stream" (type (eq 1))        → typeidx 2
//  3. type enum error-code                        → typeidx 3
//  4. export "error-code" (type (eq 3))          → typeidx 4
//  5. type own<input-stream=2>                    → typeidx 5
//  6. type borrow<descriptor=0>                   → typeidx 6
//  7. type result<ok=5, err=4>                    → typeidx 7
//  8. type func(self: 6, offset: u64) -> 7        → typeidx 8
//  9. export "[method]descriptor.read-via-stream" (func 8)
func WasiFilesystemTypesReadViaStreamInstanceTypeBody(outerInputStreamTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0a}
	// 0: descriptor resource
	body = append(body, ExportSubResourceDecl("descriptor")...)
	// 1: alias outer input-stream
	body = append(body, OuterAliasTypeDecl(1, outerInputStreamTypeidx)...)
	// 2: export "input-stream" (eq 1)
	body = append(body, ExportTypeEqDecl("input-stream", 1)...)
	// 3: enum error-code
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiFilesystemErrorCodeNames)...)
	// 4: export "error-code" (eq 3)
	body = append(body, ExportTypeEqDecl("error-code", 3)...)
	// 5: own<input-stream=2>
	body = append(body, 0x01, 0x69, 0x02)
	// 6: borrow<descriptor=0>
	body = append(body, 0x01, 0x68, 0x00)
	// 7: result<ok=5, err=4>
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(5, 4)...)
	// 8: func(self: borrow 6, offset: u64) -> typeidx 7
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x06, 'o', 'f', 'f', 's', 'e', 't', CValtypeU64,
		0x00, 0x07,
	)
	// 9: export "[method]descriptor.read-via-stream" (func 8)
	body = append(body, 0x04, 0x00, byte(len("[method]descriptor.read-via-stream")))
	body = append(body, "[method]descriptor.read-via-stream"...)
	body = append(body, 0x01, 0x08)
	return body
}

// WasiFilesystemTypesWriteViaStreamInstanceTypeBody is the
// write-side counterpart of
// WasiFilesystemTypesReadViaStreamInstanceTypeBody: declares
// `descriptor` + `error-code` + the `write-via-stream` method:
//
//	write-via-stream: func(self: borrow<descriptor>, offset: u64)
//	    -> result<own<output-stream>, error-code>
//
// `output-stream` is outer-aliased from wasi:io/streams
// (`outerOutputStreamTypeidx`). The file-write path opens a
// descriptor, then write-via-stream turns it into an output-stream
// that blocking-write-and-flush drains. Same 10-decl shape as the
// read body, only the stream resource name + method name differ.
func WasiFilesystemTypesWriteViaStreamInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0a}
	// 0: descriptor resource
	body = append(body, ExportSubResourceDecl("descriptor")...)
	// 1: alias outer output-stream
	body = append(body, OuterAliasTypeDecl(1, outerOutputStreamTypeidx)...)
	// 2: export "output-stream" (eq 1)
	body = append(body, ExportTypeEqDecl("output-stream", 1)...)
	// 3: enum error-code
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiFilesystemErrorCodeNames)...)
	// 4: export "error-code" (eq 3)
	body = append(body, ExportTypeEqDecl("error-code", 3)...)
	// 5: own<output-stream=2>
	body = append(body, 0x01, 0x69, 0x02)
	// 6: borrow<descriptor=0>
	body = append(body, 0x01, 0x68, 0x00)
	// 7: result<ok=5, err=4>
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(5, 4)...)
	// 8: func(self: borrow 6, offset: u64) -> typeidx 7
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x06, 'o', 'f', 'f', 's', 'e', 't', CValtypeU64,
		0x00, 0x07,
	)
	// 9: export "[method]descriptor.write-via-stream" (func 8)
	body = append(body, 0x04, 0x00, byte(len("[method]descriptor.write-via-stream")))
	body = append(body, "[method]descriptor.write-via-stream"...)
	body = append(body, 0x01, 0x08)
	return body
}

// WasiFilesystemTypesOpenAtInstanceTypeBody returns the
// type-section body for a `wasi:filesystem/types@0.2.0` instance
// type declaring `descriptor`, `error-code`, the path/open/
// descriptor `flags` types, and the `open-at` method:
//
//	open-at: func(self: borrow<descriptor>, path-flags: path-flags,
//	    path: string, open-flags: open-flags,
//	    flags: descriptor-flags) -> result<own<descriptor>, error-code>
//
// Self-contained — descriptor is the local resource and the flag /
// error-code types are local enums/flags, so no outer alias. This
// is the method the file-open path starts with: a preopen
// descriptor's open-at yields the file descriptor that
// read-via-stream / write-via-stream then turn into a stream.
//
// Inside-instance decl list (14 decls):
//
//  0. export "descriptor" (sub resource)         → typeidx 0
//  1. type enum error-code                         → typeidx 1
//  2. export "error-code" (type (eq 1))           → typeidx 2
//  3. type flags path-flags                        → typeidx 3
//  4. export "path-flags" (type (eq 3))           → typeidx 4
//  5. type flags open-flags                        → typeidx 5
//  6. export "open-flags" (type (eq 5))           → typeidx 6
//  7. type flags descriptor-flags                  → typeidx 7
//  8. export "descriptor-flags" (type (eq 7))     → typeidx 8
//  9. type own<descriptor=0>                       → typeidx 9
//  10. type borrow<descriptor=0>                   → typeidx 10
//  11. type result<ok=9, err=2>                    → typeidx 11
//  12. type func(self,path-flags,path,open-flags,flags) -> 11 → typeidx 12
//  13. export "[method]descriptor.open-at" (func 12)
func WasiFilesystemTypesOpenAtInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x0e}
	// 0: descriptor resource
	body = append(body, ExportSubResourceDecl("descriptor")...)
	// 1: enum error-code; 2: export
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiFilesystemErrorCodeNames)...)
	body = append(body, ExportTypeEqDecl("error-code", 1)...)
	// 3: flags path-flags; 4: export
	body = append(body, 0x01)
	body = append(body, InnerTypeFlags([]string{"symlink-follow"})...)
	body = append(body, ExportTypeEqDecl("path-flags", 3)...)
	// 5: flags open-flags; 6: export
	body = append(body, 0x01)
	body = append(body, InnerTypeFlags([]string{"create", "directory", "exclusive", "truncate"})...)
	body = append(body, ExportTypeEqDecl("open-flags", 5)...)
	// 7: flags descriptor-flags; 8: export
	body = append(body, 0x01)
	body = append(body, InnerTypeFlags([]string{
		"read", "write", "file-integrity-sync", "data-integrity-sync",
		"requested-write-sync", "mutate-directory",
	})...)
	body = append(body, ExportTypeEqDecl("descriptor-flags", 7)...)
	// 9: own<descriptor=0>
	body = append(body, 0x01, 0x69, 0x00)
	// 10: borrow<descriptor=0>
	body = append(body, 0x01, 0x68, 0x00)
	// 11: result<ok=9, err=2>
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(9, 2)...)
	// 12: func(self: borrow 10, path-flags: 4, path: string,
	//          open-flags: 6, flags: 8) -> typeidx 11. The named
	//          flag params reference the EXPORTED type aliases
	//          (4/6/8), not the raw flags defvaltypes (3/5/7) —
	//          an import's public signature can only name exported
	//          types (mirrors read-via-stream using the exported
	//          input-stream / error-code typeidxs).
	body = append(body,
		0x01, 0x40, 0x05,
		0x04, 's', 'e', 'l', 'f', 0x0a,
		0x0a, 'p', 'a', 't', 'h', '-', 'f', 'l', 'a', 'g', 's', 0x04,
		0x04, 'p', 'a', 't', 'h', CValtypeString,
		0x0a, 'o', 'p', 'e', 'n', '-', 'f', 'l', 'a', 'g', 's', 0x06,
		0x05, 'f', 'l', 'a', 'g', 's', 0x08,
		0x00, 0x0b,
	)
	// 13: export "[method]descriptor.open-at" (func 12)
	body = append(body, 0x04, 0x00, byte(len("[method]descriptor.open-at")))
	body = append(body, "[method]descriptor.open-at"...)
	body = append(body, 0x01, 0x0c)
	return body
}

// WasiFilesystemTypesReadPathInstanceTypeBody is the wasi:filesystem/types
// instance type the file-READ wrap imports: descriptor + error-code + the
// three flag types + open-at + read-via-stream, with input-stream
// outer-aliased from wasi:io/streams.
func WasiFilesystemTypesReadPathInstanceTypeBody(outerInputStreamTypeidx uint32) []byte {
	b := &instTypeBuilder{}
	v := fsPrelude(b, fsStreams{in: outerInputStreamTypeidx, needIn: true})
	fsOpenAt(b, v)
	fsViaStream(b, v, "read-via-stream", v.rIn)
	return b.body()
}

// WasiFilesystemTypesReadWritePathInstanceTypeBody is the combined
// read+write instance type — descriptor + open-at + read-via-stream AND
// write-via-stream — for a program that reads one file and writes another.
func WasiFilesystemTypesReadWritePathInstanceTypeBody(inT, outT uint32) []byte {
	b := &instTypeBuilder{}
	v := fsPrelude(b, fsStreams{in: inT, out: outT, needIn: true, needOut: true})
	fsOpenAt(b, v)
	fsViaStream(b, v, "read-via-stream", v.rIn)
	fsViaStream(b, v, "write-via-stream", v.rOut)
	return b.body()
}

// WasiFilesystemTypesReadWriteAppendPathInstanceTypeBody adds
// append-via-stream to the read+write shape — the widest of the
// stream-based filesystem instance types.
func WasiFilesystemTypesReadWriteAppendPathInstanceTypeBody(inT, outT uint32) []byte {
	b := &instTypeBuilder{}
	v := fsPrelude(b, fsStreams{in: inT, out: outT, needIn: true, needOut: true})
	fsOpenAt(b, v)
	fsViaStream(b, v, "read-via-stream", v.rIn)
	fsViaStream(b, v, "write-via-stream", v.rOut)
	fsViaStream(b, v, "append-via-stream", v.rOut)
	return b.body()
}

// WasiFilesystemTypesWritePathInstanceTypeBody is the write-side
// counterpart of WasiFilesystemTypesReadPathInstanceTypeBody: descriptor +
// open-at + write-via-stream over an outer-aliased output-stream.
func WasiFilesystemTypesWritePathInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	b := &instTypeBuilder{}
	v := fsPrelude(b, fsStreams{out: outerOutputStreamTypeidx, needOut: true})
	fsOpenAt(b, v)
	fsViaStream(b, v, "write-via-stream", v.rOut)
	return b.body()
}

// WasiFilesystemTypesAppendPathInstanceTypeBody is the append-side
// sibling: descriptor + open-at + append-via-stream, which unlike the
// read / write pair takes no offset.
func WasiFilesystemTypesAppendPathInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	b := &instTypeBuilder{}
	v := fsPrelude(b, fsStreams{out: outerOutputStreamTypeidx, needOut: true})
	fsOpenAt(b, v)
	fsViaStream(b, v, "append-via-stream", v.rOut)
	return b.body()
}

// WasiFilesystemPreopensInstanceTypeBody returns the type-section
// body for the `wasi:filesystem/preopens@0.2.0` instance type —
// `get-directories: func() -> list<tuple<own<descriptor>, string>>`,
// the preopened (descriptor, mount-path) pairs a component starts
// with. The descriptor resource lives in wasi:filesystem/types, so
// it's pulled in via an outer alias of `outerDescriptorTypeidx`
// (the io/error → io/streams cross-interface resource pattern).
//
// The returned list<tuple<own<descriptor>, string>> is a
// variable-size value, so its canon-lower needs memory + realloc.
//
// Inside-instance decl list (7 decls):
//
//  0. alias outer 1 <outerDescriptorTypeidx>     → typeidx 0
//  1. export "descriptor" (type (eq 0))          → typeidx 1
//  2. type own<typeidx 1>                         → typeidx 2
//  3. type tuple<own=2, string>                   → typeidx 3
//  4. type list<typeidx 3>                        → typeidx 4
//  5. type func() -> typeidx 4                     → typeidx 5
//  6. export "get-directories" (func 5)
func WasiFilesystemPreopensInstanceTypeBody(outerDescriptorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x07}
	// decl 0: alias outer 1 <descriptor>
	body = append(body, OuterAliasTypeDecl(1, outerDescriptorTypeidx)...)
	// decl 1: export "descriptor" (type (eq 0))
	body = append(body, ExportTypeEqDecl("descriptor", 0)...)
	// decl 2: type own<typeidx 1>
	body = append(body, 0x01, 0x69, 0x01)
	// decl 3: type tuple<own=2, string>
	body = append(body, 0x01, 0x6f, 0x02, 0x02, CValtypeString)
	// decl 4: type list<typeidx 3>
	body = append(body, 0x01, 0x70, 0x03)
	// decl 5: type func() -> typeidx 4
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x04)
	// decl 6: export "get-directories" (func 5)
	body = append(body, 0x04, 0x00, byte(len("get-directories")))
	body = append(body, "get-directories"...)
	body = append(body, 0x01, 0x05)
	return body
}

// WasiCliStdoutInstanceTypeBody returns the type-section body
// bytes for the `wasi:cli/stdout@0.2.0` instance type. The
// interface declares `get-stdout: func() -> output-stream` where
// `output-stream` is the resource type defined by
// `wasi:io/streams`.
//
// `outerOutputStreamTypeidx` is the top-level component-type
// index where the output-stream resource was surfaced via
// `PutAliasSectionInstanceExportType` after importing
// wasi:io/streams. The caller is responsible for ensuring that
// alias landed at the index passed here.
//
// Inside-instance decl list (5 decls):
//
//  0. alias outer 1 <outerOutputStreamTypeidx>   -- pulls the
//     output-stream type into inner typeidx 0.
//  1. export "output-stream" (type (eq 0))       -- re-exports
//     the aliased type as inner typeidx 1.
//  2. type (own 1)                               -- own<output-stream>
//     at inner typeidx 2.
//  3. type (func () -> typeidx 2)                -- the
//     get-stdout functype at inner typeidx 3.
//  4. export "get-stdout" (func 3)
func WasiCliStdoutInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	return wasiCliStreamGetterInstanceTypeBody(outerOutputStreamTypeidx, "get-stdout", "output-stream")
}

// WasiCliStderrInstanceTypeBody is the wasi:cli/stderr@0.2.0
// sibling of WasiCliStdoutInstanceTypeBody. Same instance-type
// shape — `get-stderr: func() -> output-stream` — only the
// exported function name differs.
func WasiCliStderrInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	return wasiCliStreamGetterInstanceTypeBody(outerOutputStreamTypeidx, "get-stderr", "output-stream")
}

// WasiClocksWallClockInstanceTypeBody returns the type-section
// body bytes for the `wasi:clocks/wall-clock@0.2.0` instance
// type. The interface declares
// `now: func() -> datetime` where `datetime` is the record
// `{ seconds: u64; nanoseconds: u32 }` — no resources, so
// (unlike the stdio interfaces) there's no wasi:io/error
// dependency or outer alias.
//
// Inside-instance decl list (4 decls):
//
//  0. type record { seconds: u64; nanoseconds: u32 }  → typeidx 0
//  1. export "datetime" (type (eq 0))                 → typeidx 1
//  2. type func () -> typeidx 1                        → typeidx 2
//  3. export "now" (func 2)
func WasiClocksWallClockInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x04}
	// decl 0: type record { seconds: u64; nanoseconds: u32 }
	body = append(body, 0x01) // type decl
	body = append(body, 0x72, 0x02)
	body = append(body, 0x07, 's', 'e', 'c', 'o', 'n', 'd', 's', CValtypeU64)
	body = append(body, 0x0b, 'n', 'a', 'n', 'o', 's', 'e', 'c', 'o', 'n', 'd', 's', CValtypeU32)
	// decl 1: export "datetime" (type (eq 0))
	body = append(body, ExportTypeEqDecl("datetime", 0)...)
	// decl 2: type func() -> typeidx 1 (the exported datetime)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x01)
	// decl 3: export "now" (func 2)
	body = append(body, 0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x02)
	return body
}

// WasiCliEnvironmentArgsInstanceTypeBody is the
// wasi:cli/environment@0.2.0 instance type declaring just
// `get-arguments: func() -> list<string>` — the import Lang's
// `args()` builtin needs. The host's real wasi:cli/environment
// interface exports more (get-environment, get-initial-cwd), but
// an import's instance type only needs to list what we consume
// (structural subtyping); the host provides the superset.
//
// No resources / outer aliases — list<string> is a plain
// defvaltype. Inside-instance decl list (3 decls):
//
//  0. type list<string>                  → typeidx 0
//  1. type func() -> typeidx 0           → typeidx 1
//  2. export "get-arguments" (func 1)
//
// The returned list<string> is a variable-size canonical-ABI
// value, so its canon-lower needs memory + realloc (the host
// allocates the list + each string's bytes through cabi_realloc).
func WasiCliEnvironmentArgsInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x03}
	// decl 0: type list<string>  (0x70 list, 0x73 string)
	body = append(body, 0x01, 0x70, CValtypeString)
	// decl 1: type func() -> typeidx 0
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x00)
	// decl 2: export "get-arguments" (func 1)
	body = append(body, 0x04, 0x00, byte(len("get-arguments")))
	body = append(body, "get-arguments"...)
	body = append(body, 0x01, 0x01)
	return body
}

// WasiCliEnvironmentGetEnvironmentInstanceTypeBody is the
// wasi:cli/environment@0.2.0 instance type declaring just
// `get-environment: func() -> list<tuple<string, string>>` — the
// import Lang's `env()` builtin needs. Each tuple is a (key, value)
// pair from a "KEY=VALUE" environment entry. Like the args body,
// an import's instance type only lists what we consume.
//
// Inside-instance decl list (4 decls):
//
//  0. type tuple<string, string>            → typeidx 0
//  1. type list<typeidx 0>                  → typeidx 1
//  2. type func() -> typeidx 1              → typeidx 2
//  3. export "get-environment" (func 2)
//
// The returned list<tuple<string, string>> is a variable-size
// canonical-ABI value, so its canon-lower needs memory + realloc.
func WasiCliEnvironmentGetEnvironmentInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x04}
	// decl 0: type tuple<string, string>  (0x6f tuple, 2 elems)
	body = append(body, 0x01, 0x6f, 0x02, CValtypeString, CValtypeString)
	// decl 1: type list<typeidx 0>
	body = append(body, 0x01, 0x70, 0x00)
	// decl 2: type func() -> typeidx 1
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x01)
	// decl 3: export "get-environment" (func 2)
	body = append(body, 0x04, 0x00, byte(len("get-environment")))
	body = append(body, "get-environment"...)
	body = append(body, 0x01, 0x02)
	return body
}

// WasiCliEnvironmentArgsAndEnvInstanceTypeBody is the
// wasi:cli/environment@0.2.0 instance type declaring BOTH
// `get-arguments: func() -> list<string>` and
// `get-environment: func() -> list<tuple<string, string>>` — for a
// program that imports both args() and env(). They share one
// interface, so they must live in a single instance type (two separate
// single-func imports of the same interface name don't compose).
//
// Inside-instance decl list (7 decls):
//
//  0. type list<string>                  → typeidx 0
//  1. type func() -> typeidx 0           → typeidx 1
//  2. export "get-arguments" (func 1)
//  3. type tuple<string, string>         → typeidx 2
//  4. type list<typeidx 2>               → typeidx 3
//  5. type func() -> typeidx 3           → typeidx 4
//  6. export "get-environment" (func 4)
//
// Func exports occupy the func index space, not the type index space,
// so the get-environment types resume numbering after the
// get-arguments functype. Both returns are variable-size, so their
// canon-lowers need memory + realloc.
func WasiCliEnvironmentArgsAndEnvInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x07}
	// decl 0: type list<string>
	body = append(body, 0x01, 0x70, CValtypeString)
	// decl 1: type func() -> typeidx 0
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x00)
	// decl 2: export "get-arguments" (func 1)
	body = append(body, 0x04, 0x00, byte(len("get-arguments")))
	body = append(body, "get-arguments"...)
	body = append(body, 0x01, 0x01)
	// decl 3: type tuple<string, string>
	body = append(body, 0x01, 0x6f, 0x02, CValtypeString, CValtypeString)
	// decl 4: type list<typeidx 2>
	body = append(body, 0x01, 0x70, 0x02)
	// decl 5: type func() -> typeidx 3
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x03)
	// decl 6: export "get-environment" (func 4)
	body = append(body, 0x04, 0x00, byte(len("get-environment")))
	body = append(body, "get-environment"...)
	body = append(body, 0x01, 0x04)
	return body
}

// WasiCliStdinInstanceTypeBody is the wasi:cli/stdin@0.2.0
// counterpart — `get-stdin: func() -> input-stream`. Same shape
// as the stdout/stderr getters, only the stream resource type
// (input-stream) and the getter name differ.
func WasiCliStdinInstanceTypeBody(outerInputStreamTypeidx uint32) []byte {
	return wasiCliStreamGetterInstanceTypeBody(outerInputStreamTypeidx, "get-stdin", "input-stream")
}

// wasiCliStreamGetterInstanceTypeBody builds the shared
// instance-type body for the wasi:cli stdio getters
// (stdout / stderr → output-stream, stdin → input-stream). Each
// declares a single `<getFuncName>: func() -> <streamType>`
// referencing the wasi:io/streams resource at `outerStreamTypeidx`.
func wasiCliStreamGetterInstanceTypeBody(outerStreamTypeidx uint32, getFuncName, streamType string) []byte {
	body := []byte{0x01, 0x42, 0x05}
	body = append(body, OuterAliasTypeDecl(1, outerStreamTypeidx)...)
	body = append(body, ExportTypeEqDecl(streamType, 0)...)
	body = append(body, 0x01, 0x69, 0x01) // type decl: own<typeidx 1>
	body = append(body,
		0x01,       // type decl
		0x40, 0x00, // functype form, vec(0) params
		0x00, 0x02, // resultlist single anonymous, valtype = typeidx 2
	)
	body = append(body, 0x04, 0x00) // export decl, exportname kind label
	body = append(body, byte(len(getFuncName)))
	body = append(body, getFuncName...)
	body = append(body, 0x01, 0x03) // externdesc func, typeidx 3
	return body
}

// WasiIoStreamsInstanceTypeBody returns the type-section body
// bytes for the `wasi:io/streams@0.2.0` instance type as it
// appears in a fd_write-shape component. Declares the
// output-stream resource, the stream-error variant, and the
// `[method]output-stream.blocking-write-and-flush` method.
//
// `outerErrorTypeidx` is the top-level component-type index
// where wasi:io/error's `error` resource was surfaced via
// `PutAliasSectionInstanceExportType` after importing
// wasi:io/error.
//
// Inside-instance decl list (11 decls), inner typeidxs in order:
//
//	0  export "output-stream" (sub resource)
//	1  alias outer 1 <outerErrorTypeidx>
//	2  export "error" (type (eq 1))
//	3  type own<2>                              -- own<error>
//	4  type variant { last-operation-failed(3), closed }
//	5  export "stream-error" (type (eq 4))
//	6  type borrow<0>                            -- borrow<output-stream>
//	7  type list<u8>
//	8  type result<_, err=5>
//	9  type func(self: 6, contents: 7) -> typeidx 8
//	10 export "[method]output-stream.blocking-write-and-flush"
//	   (func typeidx 9)
//
// The `[method]X.Y` export-name convention is what wasm-tools
// emits for resource methods; it's what wasmtime's component-
// model linker matches against the host's resource-method
// table.
func WasiIoStreamsInstanceTypeBody(outerErrorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0b}
	// decl 0: export "output-stream" (sub resource) → inner 0
	body = append(body, ExportSubResourceDecl("output-stream")...)
	// decl 1: alias outer 1 <outerErrorTypeidx> → inner 1
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...)
	// decl 2: export "error" (type (eq 1)) → inner 2
	body = append(body, ExportTypeEqDecl("error", 1)...)
	// decl 3: type own<typeidx 2> → inner 3
	body = append(body, 0x01, 0x69, 0x02)
	// decl 4: type variant { last-operation-failed(3), closed } → inner 4
	body = append(body, 0x01)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})...)
	// decl 5: export "stream-error" (type (eq 4)) → inner 5
	body = append(body, ExportTypeEqDecl("stream-error", 4)...)
	// decl 6: type borrow<typeidx 0> → inner 6
	body = append(body, 0x01)
	body = append(body, InnerTypeBorrow(0)...)
	// decl 7: type list<u8> → inner 7
	body = append(body, 0x01)
	body = append(body, InnerTypeListU8...)
	// decl 8: type result<_, err=5> → inner 8
	body = append(body, 0x01)
	body = append(body, InnerTypeResultErr(5)...)
	// decl 9: type func(self: 6, contents: 7) -> typeidx 8 → inner 9
	body = append(body,
		0x01,                           // type decl
		0x40,                           // functype form
		0x02,                           // vec(2) params
		0x04, 's', 'e', 'l', 'f', 0x06, // param "self" valtype=typeidx 6
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x07, // param "contents" valtype=typeidx 7
		0x00, 0x08, // resultlist single anonymous, valtype = typeidx 8
	)
	// decl 10: export "[method]output-stream.blocking-write-and-flush" (func 9)
	body = append(body, 0x04, 0x00, 0x2e) // export, kind=label, name-len 46
	body = append(body, "[method]output-stream.blocking-write-and-flush"...)
	body = append(body, 0x01, 0x09) // externdesc func, typeidx 9
	return body
}

// WasiIoStreamsReadWriteInstanceTypeBody declares BOTH stream
// directions in one wasi:io/streams instance type — output-stream +
// input-stream resources, the shared error / stream-error types,
// and both methods (blocking-write-and-flush, blocking-read). It's
// the io/streams import a mixed read+write program (e.g. read_line +
// print) needs. Combines WasiIoStreamsInstanceTypeBody (write) and
// WasiIoStreamsReadInstanceTypeBody (read); the extra input-stream
// resource shifts the shared types up by one slot.
//
// Decl list (16 decls): 0 output-stream, 1 input-stream, 2 alias
// error, 3 export error, 4 own<error>, 5 variant stream-error,
// 6 export stream-error, 7 borrow<output-stream>, 8
// borrow<input-stream>, 9 list<u8>, 10 result<_, stream-error>,
// 11 result<list<u8>, stream-error>, 12 func blocking-write-and-flush,
// 13 export it, 14 func blocking-read, 15 export it. (func exports
// don't consume a typeidx, so the methods are typeidx 12 and 13.)
func WasiIoStreamsReadWriteInstanceTypeBody(outerErrorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x10}                                 // 16 decls
	body = append(body, ExportSubResourceDecl("output-stream")...)   // 0
	body = append(body, ExportSubResourceDecl("input-stream")...)    // 1
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...) // 2
	body = append(body, ExportTypeEqDecl("error", 2)...)             // 3
	body = append(body, 0x01, 0x69, 0x03)                            // 4: own<error=3>
	body = append(body, 0x01)                                        // 5: variant
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x04},
		{Name: "closed"},
	})...)
	body = append(body, ExportTypeEqDecl("stream-error", 5)...) // 6
	body = append(body, 0x01)                                   // 7: borrow<output=0>
	body = append(body, InnerTypeBorrow(0)...)
	body = append(body, 0x01) // 8: borrow<input=1>
	body = append(body, InnerTypeBorrow(1)...)
	body = append(body, 0x01) // 9: list<u8>
	body = append(body, InnerTypeListU8...)
	body = append(body, 0x01) // 10: result<_, err=6>
	body = append(body, InnerTypeResultErr(6)...)
	body = append(body, 0x01) // 11: result<list=9, err=6>
	body = append(body, InnerTypeResultOkErr(9, 6)...)
	// 12: func blocking-write-and-flush(self: borrow-out=7, contents: list=9) -> 10
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x07,
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x09,
		0x00, 0x0a,
	)
	// 13: export "[method]output-stream.blocking-write-and-flush" (func 12)
	body = append(body, 0x04, 0x00, byte(len("[method]output-stream.blocking-write-and-flush")))
	body = append(body, "[method]output-stream.blocking-write-and-flush"...)
	body = append(body, 0x01, 0x0c)
	// 14: func blocking-read(self: borrow-in=8, len: u64) -> 11
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x08,
		0x03, 'l', 'e', 'n', CValtypeU64,
		0x00, 0x0b,
	)
	// 15: export "[method]input-stream.blocking-read" (func 13)
	body = append(body, 0x04, 0x00, byte(len("[method]input-stream.blocking-read")))
	body = append(body, "[method]input-stream.blocking-read"...)
	body = append(body, 0x01, 0x0d)
	return body
}

// WasiIoStreamsReadInstanceTypeBody is the read-side counterpart
// of WasiIoStreamsInstanceTypeBody: declares the input-stream
// resource + the `blocking-read` method
// (`func(len: u64) -> result<list<u8>, stream-error>`). Used by
// the fd_read wrap. Same stream-error / error-resource scaffolding
// as the write side; the differences are the input-stream resource
// name, the both-arms result (the ok arm carries the returned
// list<u8>), and the method signature (a u64 `len` param, no
// list param).
//
// Inner typeidxs match the write body through decl 7; decl 8's
// result gains an ok arm and decl 9's functype takes `len: u64`
// instead of `contents: list<u8>`.
func WasiIoStreamsReadInstanceTypeBody(outerErrorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0b}
	body = append(body, ExportSubResourceDecl("input-stream")...)    // inner 0
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...) // inner 1
	body = append(body, ExportTypeEqDecl("error", 1)...)             // inner 2
	body = append(body, 0x01, 0x69, 0x02)                            // inner 3: own<2>
	body = append(body, 0x01)                                        // inner 4: variant
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})...)
	body = append(body, ExportTypeEqDecl("stream-error", 4)...) // inner 5
	body = append(body, 0x01)                                   // inner 6: borrow<0>
	body = append(body, InnerTypeBorrow(0)...)
	body = append(body, 0x01) // inner 7: list<u8>
	body = append(body, InnerTypeListU8...)
	body = append(body, 0x01) // inner 8: result<ok=7, err=5>
	body = append(body, InnerTypeResultOkErr(7, 5)...)
	// inner 9: func(self: borrow<input-stream> typeidx 6, len: u64) -> typeidx 8
	body = append(body,
		0x01,                           // type decl
		0x40,                           // functype form
		0x02,                           // vec(2) params
		0x04, 's', 'e', 'l', 'f', 0x06, // param "self" valtype=typeidx 6
		0x03, 'l', 'e', 'n', CValtypeU64, // param "len" valtype=u64
		0x00, 0x08, // resultlist single anonymous, valtype = typeidx 8
	)
	// decl 10: export "[method]input-stream.blocking-read" (func 9)
	body = append(body, 0x04, 0x00, byte(len("[method]input-stream.blocking-read")))
	body = append(body, "[method]input-stream.blocking-read"...)
	body = append(body, 0x01, 0x09)
	return body
}

// PutImportSectionOneInstance emits an import section with one
// label-form entry naming an instance import of the given typeidx.
// Mirrors std/wasm/component's `put_import_section_one_instance`.
func PutImportSectionOneInstance(buf []byte, name string, instanceTypeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // importname kind = label
	body = putName(body, name)
	body = append(body, 0x05) // externdesc kind = instance
	body = leb128.UlebU64(body, uint64(instanceTypeidx))
	return wrapSection(buf, SectionImport, body)
}

// PutAliasSectionInstanceExportFunc emits an alias section that
// surfaces a function exported by a component instance as a
// top-level component-level func. Mirrors
// `put_alias_section_instance_export_func`.
func PutAliasSectionInstanceExportFunc(buf []byte, instanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x01)      // sort = func
	body = append(body, 0x00)      // target: from-instance-export
	body = leb128.UlebU64(body, uint64(instanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

// PutAliasSectionInstanceExportType emits an alias section that
// surfaces a TYPE exported by a component instance as a top-level
// component-level type. Used when one imported interface
// references a type declared in another — e.g.
// wasi:io/streams's `error` resource handle references the
// `error` resource declared inside the wasi:io/error import.
//
// Wire shape:
//
//	06 <size>      -- section id 6 (alias), uleb size
//	01             -- vec(1) aliases
//	03             -- sort = type
//	00             -- target: from-instance-export
//	<instanceIdx>  -- uleb
//	<name>         -- uleb len + bytes (the type's export name in the instance)
//
// The aliased type lands at the next free top-level component-type
// index. Pair with an outer-alias decl inside a subsequent
// instance type body to reference it from a nested scope.
func PutAliasSectionInstanceExportType(buf []byte, instanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x03)      // sort = type
	body = append(body, 0x00)      // target: from-instance-export
	body = leb128.UlebU64(body, uint64(instanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

// PutCanonSectionLowerNoOpts emits a canon section with one
// canon-lower entry (no opts). Mirrors
// `put_canon_section_lower_no_opts`.
func PutCanonSectionLowerNoOpts(buf []byte, funcIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x01)      // canon-lower
	body = append(body, 0x00)      // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 0) // no opts
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonResourceDrop appends a canon section with a single
// `canon resource.drop <typeidx>` entry, producing a core func that
// drops a handle of the given imported resource type. resourceTypeidx
// is the component-level type index of the resource (e.g. an
// outer-aliased `pollable` / `tcp-socket`). Core modules import the
// `[resource-drop]<name>` builtin as a `(param i32) -> ()` func; this
// supplies the matching core func to instantiate them with.
//
// Wire shape: 08 <size> | 01 (vec) | 03 (resource.drop) | <typeidx>.
func PutCanonResourceDrop(buf []byte, resourceTypeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x03)      // canon resource.drop
	body = leb128.UlebU64(body, uint64(resourceTypeidx))
	return wrapSection(buf, SectionCanon, body)
}

// The WASI Preview-3 waitable-set / subtask canon builtins — the await state
// machine a guest uses to drive a *pending* (non-synchronously-completing)
// async import (docs/WASI-PREVIEW3-ASYNC-PLAN.md). The canon-builtin opcodes
// are byte-verified against wasm-tools 1.240's `dump`:
//
//	waitable-set.new  = 0x1f                          -> () -> i32 (set handle)
//	waitable-set.wait = 0x20 <cancellable> <memidx>   -> (set, ptr) -> i32 (event)
//	waitable-set.poll = 0x21 <cancellable> <memidx>   -> (set, ptr) -> i32
//	waitable-set.drop = 0x22                          -> (set) -> ()
//	waitable.join     = 0x23                          -> (waitable, set) -> ()
//	subtask.drop      = 0x0d                          -> (subtask) -> ()
//
// These are the encoder layer for the pending-await epic; the guest await loop
// (wasmbin) + a genuinely-deferring provider follow.

// PutCanonWaitableSetNew emits `canon waitable-set.new` (0x1f).
func PutCanonWaitableSetNew(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x1f)      // waitable-set.new
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonWaitableSetWait emits `canon waitable-set.wait` (0x20) with the
// (non-cancellable) `memory` option — the blocking wait that writes the
// completed event into `memIdx` and returns its code.
func PutCanonWaitableSetWait(buf []byte, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x20)      // waitable-set.wait
	body = append(body, 0x00)      // cancellable: false
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonWaitableSetDrop emits `canon waitable-set.drop` (0x22).
func PutCanonWaitableSetDrop(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x22)      // waitable-set.drop
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonWaitableJoin emits `canon waitable.join` (0x23) — adds a waitable
// (e.g. a subtask) to a waitable-set.
func PutCanonWaitableJoin(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x23)      // waitable.join
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSubtaskDrop emits `canon subtask.drop` (0x0d) — releases a finished
// subtask handle returned by an async lower.
func PutCanonSubtaskDrop(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x0d)      // subtask.drop
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonThreadYield emits `canon thread.yield` (0x0c, non-cancellable) — the
// cooperative yield a *deferring* async export calls before delivering its
// result, so the caller's async lower returns a STARTED (pending) status and
// the await loop runs. (The component-model spells this `thread.yield`, not
// `canon yield`.) Byte-verified against wasm-tools 1.240 (`0x0c 0x00`).
func PutCanonThreadYield(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x0c)      // thread.yield
	body = append(body, 0x00)      // cancellable: false
	return wrapSection(buf, SectionCanon, body)
}

// The WASI Preview-3 `future<T>` / `stream<T>` canon builtins. All opcodes +
// option encodings byte-verified against wasm-tools 1.240 (see
// docs/WASI-PREVIEW3-ASYNC-PLAN.md). `typeidx` is the component type index of the
// `future<T>` / `stream<T>` defined type. `.new` carries no options; `.read` /
// `.write` carry the canonical `[async, memory <idx>]` options the async data
// transfer needs (async so a not-yet-ready transfer returns a pending status; the
// `memory` option locates the element buffer).

// PutCanonFutureNew emits `canon future.new` (0x15) — allocates a fresh future,
// returning the packed writable/readable handle pair.
func PutCanonFutureNew(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x15)      // future.new
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonFutureRead emits `canon future.read` (0x16) with `[async, memory]` —
// reads the future's value into `memIdx`, returning a status.
func PutCanonFutureRead(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x16)      // future.read
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)              // 2 options
	body = append(body, 0x06)                   // async
	body = append(body, 0x03)                   // memory
	body = leb128.UlebU64(body, uint64(memIdx)) // memidx
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonFutureWrite emits `canon future.write` (0x17) with `[async, memory]` —
// writes the value at `memIdx` into the future (resolving it), returning a status.
func PutCanonFutureWrite(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x17)      // future.write
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)              // 2 options
	body = append(body, 0x06)                   // async
	body = append(body, 0x03)                   // memory
	body = leb128.UlebU64(body, uint64(memIdx)) // memidx
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonStreamNew emits `canon stream.new` (0x0e) — the `stream<T>` counterpart
// of future.new.
func PutCanonStreamNew(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x0e)      // stream.new
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonStreamRead emits `canon stream.read` (0x0f) with `[async, memory]`.
func PutCanonStreamRead(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x0f)      // stream.read
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)              // 2 options
	body = append(body, 0x06)                   // async
	body = append(body, 0x03)                   // memory
	body = leb128.UlebU64(body, uint64(memIdx)) // memidx
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonStreamWrite emits `canon stream.write` (0x10) with `[async, memory]`.
func PutCanonStreamWrite(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x10)      // stream.write
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)              // 2 options
	body = append(body, 0x06)                   // async
	body = append(body, 0x03)                   // memory
	body = leb128.UlebU64(body, uint64(memIdx)) // memidx
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonStreamDropReadable emits `canon stream.drop-readable` (0x13) — releases
// the readable end of a `stream<T>` (the consumer calls it after EOF). Core sig
// `(handle) -> ()`. Byte-verified against wasm-tools 1.240.
func PutCanonStreamDropReadable(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x13)      // stream.drop-readable
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonStreamDropWritable emits `canon stream.drop-writable` (0x14) — releases
// the writable end of a `stream<T>`, signalling EOF to the reader (the producer
// calls it after its final awaited write). Core sig `(handle) -> ()`.
// Byte-verified against wasm-tools 1.240.
func PutCanonStreamDropWritable(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x14)      // stream.drop-writable
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLowerWithMemory emits a canon section with one
// canon-lower entry that carries a single `memory` canonical-ABI
// option. The memory option is needed when the lowered function
// takes or returns a value whose canonical-ABI shape includes a
// pointer into linear memory (e.g. list<u8>, indirect-return
// record, string).
//
// Opts encoding (vec of canonopt):
//
//	03 m:<core memidx>    -- memory
//
// Pair with the trampoline-table pattern when the lowered func
// is referenced by a core module that also exports the memory —
// the canon-lower has to come AFTER the core instantiation that
// produces the memory, which means the core module's import
// can't refer to the lowered func directly. Wasm-tools resolves
// this by emitting a trampoline core module with a funcref
// table; this composer doesn't enforce that arrangement, only
// emits the canon-lower bytes.
func PutCanonSectionLowerWithMemory(buf []byte, funcIdx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) canons
	body = append(body, 0x01)      // canon-lower
	body = append(body, 0x00)      // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 1) // opts vec(1)
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLowerAsync emits a canon-lower entry carrying the
// `async` (0x06) + `memory` (0x03) canonical options — the WASI
// Preview-3 import side: lowering an imported `async func` so the guest
// can call it and await the result. `memory` is REQUIRED for an async
// lower (the lowered call writes the subtask/return info into linear
// memory; a bare async lower validate-fails "canonical option `memory`
// is required"). For an import that completes synchronously, the
// lowered core call writes the result into the return area and the
// guest reads it directly — no waitable-set loop needed; a pending
// import additionally needs `waitable-set.wait`. Byte-verified against
// a nested-component await that returns its result under
// `wasmtime -W component-model-async,component-model-async-stackful`.
// See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonSectionLowerAsync(buf []byte, funcIdx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) canons
	body = append(body, 0x01)      // canon-lower
	body = append(body, 0x00)      // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 2) // opts vec(2)
	body = append(body, 0x06)      // canonopt: async
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLowerAsyncRealloc is PutCanonSectionLowerAsync with the
// additional `realloc` (0x04) canonical option — the WASI Preview-3 async
// import side for a result that carries linear-memory data (a `string` /
// `list<T>`). A scalar async lower needs only `[async, memory]` (the result is
// a fixed-size scalar written into the return area), but a string/list result
// flattens to a `(ptr, len)` whose backing bytes the canonical ABI materialises
// in the guest's memory via `realloc` — so the lower carries
// `[async, memory, realloc]`. The realloc func is the guest's exported
// cabi_realloc (aliased through the same trampoline as the memory). For a
// synchronously-completing import the host writes the bytes + the (ptr,len)
// into the return area before the lowered call returns, so the guest reads them
// inline (no waitable loop). See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonSectionLowerAsyncRealloc(buf []byte, funcIdx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) canons
	body = append(body, 0x01)      // canon-lower
	body = append(body, 0x00)      // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 3) // opts vec(3)
	body = append(body, 0x06)      // canonopt: async
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04) // canonopt: realloc
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLowerWithMemoryRealloc emits a canon-lower entry
// carrying both `memory` and `realloc` canonical-ABI options. The
// realloc option is required when the lowered function needs the
// canonical-ABI machinery to allocate space in linear memory at
// the boundary — typically when receiving a host-supplied list /
// string from the host through a return value (canon LOWER of a
// function returning a heap-allocated value).
//
// Most preview-2 imports with list params only need memory (the
// caller provides the bytes); imports with list / string returns
// (or with results carrying lists in their payload) need both.
//
// Opts encoding (vec of canonopt, sorted by canonical-ABI
// convention but the binary form is unordered):
//
//	03 m:<core memidx>      -- memory
//	04 f:<core funcidx>     -- realloc
func PutCanonSectionLowerWithMemoryRealloc(buf []byte, funcIdx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) canons
	body = append(body, 0x01)      // canon-lower
	body = append(body, 0x00)      // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 2) // opts vec(2)
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04) // canonopt: realloc
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCoreInstanceSectionFromOneFuncExport emits a core-instance
// section with one "instance-from-exports" entry packaging a
// single core func. Mirrors
// `put_core_instance_section_from_one_func_export`.
func PutCoreInstanceSectionFromOneFuncExport(buf []byte, exportName string, coreFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x01)      // form: from-exports
	body = leb128.UlebU64(body, 1) // vec(1) exports
	body = putName(body, exportName)
	body = append(body, CoreSortFunc)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	return wrapSection(buf, SectionCoreInstance, body)
}

// CoreInstanceExport describes one (name, sort, idx) entry in a
// from-exports core instance. The sort byte uses CoreSort*
// constants (0x00 func / 0x01 table / 0x02 memory / 0x03 global).
type CoreInstanceExport struct {
	Name string
	Sort byte
	Idx  uint32
}

// PutCoreInstanceSectionFromExports emits a core-instance section
// with one "instance-from-exports" entry packaging N core
// entities of mixed sorts. Generalisation of
// PutCoreInstanceSectionFromOneFuncExport — needed for the
// fd_write fixup-module's instance arg, which packages both a
// table and a func into the same instance.
func PutCoreInstanceSectionFromExports(buf []byte, exports []CoreInstanceExport) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x01)      // form: from-exports
	body = leb128.UlebU64(body, uint64(len(exports)))
	for _, e := range exports {
		body = putName(body, e.Name)
		body = append(body, e.Sort)
		body = leb128.UlebU64(body, uint64(e.Idx))
	}
	return wrapSection(buf, SectionCoreInstance, body)
}

// PutAliasSectionCoreExport emits an alias section for a single
// core-instance export of any sort (func / table / memory /
// global). Generalisation of PutAliasSectionCoreExportFunc.
func PutAliasSectionCoreExport(buf []byte, coreSort byte, coreInstanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // sort = core
	body = append(body, coreSort)  // core-sort byte
	body = append(body, 0x01)      // target: from-core-instance-export
	body = leb128.UlebU64(body, uint64(coreInstanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

// PutCoreInstanceSectionInstantiateWithInstanceArgs emits a
// core-instance section with one "instantiate" entry passing N
// instance args. Mirrors
// `put_core_instance_section_instantiate_with_instance_args`.
func PutCoreInstanceSectionInstantiateWithInstanceArgs(buf []byte, moduleIdx uint32, argNames []string, instanceIdxs []uint32) []byte {
	if len(argNames) != len(instanceIdxs) {
		panic("component: argNames and instanceIdxs must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x00)      // form: instantiate
	body = leb128.UlebU64(body, uint64(moduleIdx))
	body = leb128.UlebU64(body, uint64(len(argNames)))
	for i := range argNames {
		body = putName(body, argNames[i])
		body = append(body, CoreSortInstance)
		body = leb128.UlebU64(body, uint64(instanceIdxs[i]))
	}
	return wrapSection(buf, SectionCoreInstance, body)
}

// PutCoreInstanceSectionInstantiate emits a core-instance section
// with one "instantiate" entry, no args. The simplest core-instance
// shape — for self-contained core modules with no imports. Mirrors
// `put_core_instance_section_instantiate`.
func PutCoreInstanceSectionInstantiate(buf []byte, moduleIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x00)      // form: instantiate
	body = leb128.UlebU64(body, uint64(moduleIdx))
	body = leb128.UlebU64(body, 0) // no args
	return wrapSection(buf, SectionCoreInstance, body)
}

// PutAliasSectionCoreExportFunc emits an alias section that
// surfaces a core function exported by a core instance as a
// top-level core-sort func. Mirrors
// `put_alias_section_core_export_func`.
func PutAliasSectionCoreExportFunc(buf []byte, coreInstanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // sort = core
	body = append(body, 0x00)      // core-sort = func
	body = append(body, 0x01)      // target: from-core-instance-export
	body = leb128.UlebU64(body, uint64(coreInstanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

// Component functype form bytes. The canonical (synchronous) functype
// discriminant is 0x40; the component-model-async proposal adds an async
// functype form 0x43, required for any component function type referenced by
// a `canon lift async` / `canon lower async` (wasm-tools >= ~1.25x / wasmtime
// >= v40 reject the `async` canonical option on a plain 0x40 functype with
// "the `async` canonical option requires an async function type"). The two
// encodings are otherwise byte-identical — same params + resultlist grammar —
// so the async emitters below differ from their sync siblings only in this
// leading tag. Verified byte-for-byte against wasm-tools 1.253 and run under
// wasmtime v46 (docs/WASI-PREVIEW3-ASYNC-PLAN.md).
const (
	cFunctypeSync  = 0x40
	cFunctypeAsync = 0x43
)

// PutTypeSectionOneFunc emits a component-level type section
// containing one functype with N params + a single anonymous
// result. Mirrors `put_type_section_one_func`.
func PutTypeSectionOneFunc(buf []byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	return putTypeSectionOneFuncTag(buf, cFunctypeSync, paramNames, paramValtypes, resultValtype)
}

// PutTypeSectionOneFuncAsync is PutTypeSectionOneFunc with the async functype
// form (0x43) — for a component function type referenced by a `canon lift/lower
// async`. See the cFunctype* constants.
func PutTypeSectionOneFuncAsync(buf []byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	return putTypeSectionOneFuncTag(buf, cFunctypeAsync, paramNames, paramValtypes, resultValtype)
}

func putTypeSectionOneFuncTag(buf []byte, tag byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) types
	body = append(body, tag)       // functype form (sync 0x40 / async 0x43)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	body = append(body, 0x00) // resultlist: single anonymous
	body = append(body, resultValtype)
	return wrapSection(buf, SectionType, body)
}

// PutTypeSectionOneDefined emits a component-level type section containing one
// defined value type whose body is `defBody` (e.g. InnerTypeList(CValtypeS32)
// for `list<s32>`). The new type lands at the next component type index. Used by
// the P6 export lift to declare a `list<T>` result/param type before the
// functype that references it (docs/WIT-BRING-YOUR-OWN.md).
func PutTypeSectionOneDefined(buf []byte, defBody []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) types
	body = append(body, defBody...)
	return wrapSection(buf, SectionType, body)
}

// PutTypeSectionOneFuncResultIdx is PutTypeSectionOneFunc for a function whose
// single anonymous result is a *defined* type referenced by index (e.g. a
// `list<T>`), not a primitive. The result valtype is encoded as a signed LEB128
// (`s33`) — a type index ≥ 64 needs more than one byte (its high payload bit
// must stay clear so it isn't read as a negative primitive code), which the
// single-byte append in PutTypeSectionOneFunc gets wrong. P6 composite exports.
func PutTypeSectionOneFuncResultIdx(buf []byte, paramNames []string, paramValtypes []byte, resultIdx uint32) []byte {
	return putTypeSectionOneFuncResultIdxTag(buf, cFunctypeSync, paramNames, paramValtypes, resultIdx)
}

// PutTypeSectionOneFuncResultIdxAsync is PutTypeSectionOneFuncResultIdx with the
// async functype form (0x43) — e.g. the stream lift's `() -> stream<elem>`.
func PutTypeSectionOneFuncResultIdxAsync(buf []byte, paramNames []string, paramValtypes []byte, resultIdx uint32) []byte {
	return putTypeSectionOneFuncResultIdxTag(buf, cFunctypeAsync, paramNames, paramValtypes, resultIdx)
}

func putTypeSectionOneFuncResultIdxTag(buf []byte, tag byte, paramNames []string, paramValtypes []byte, resultIdx uint32) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) types
	body = append(body, tag)       // functype form (sync 0x40 / async 0x43)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	body = append(body, 0x00)                     // resultlist: single anonymous
	body = leb128.SlebI64(body, int64(resultIdx)) // valtype = typeidx (s33)
	return wrapSection(buf, SectionType, body)
}

// leb128SlebBytes returns the sleb-encoded (s33) bytes of a defined-type index
// — the valtype form a functype param/result uses to reference a list/record
// type by index. (≥ 64 needs more than one byte.)
func leb128SlebBytes(idx uint32) []byte { return leb128.SlebI64(nil, int64(idx)) }

// PutTypeSectionOneFuncGeneral emits a functype where each parameter and the
// single anonymous result is supplied as pre-encoded valtype bytes — either a
// primitive's single byte (CValtype*) or the sleb-encoded (s33) index of a
// defined type emitted earlier (a `list<T>`, record, …). It generalises
// PutTypeSectionOneFunc (all-prim) and PutTypeSectionOneFuncResultIdx
// (index result): the P6 composite param/result exports need a mix. The caller
// emits any referenced defined types first so the indices resolve.
func PutTypeSectionOneFuncGeneral(buf []byte, paramNames []string, paramVals [][]byte, resultVal []byte) []byte {
	return putTypeSectionOneFuncGeneralTag(buf, cFunctypeSync, paramNames, paramVals, resultVal)
}

// PutTypeSectionOneFuncGeneralAsync is PutTypeSectionOneFuncGeneral with the
// async functype form (0x43) — for an async lift/lower whose param or result is
// a defined type referenced by index (tuple / stream param).
func PutTypeSectionOneFuncGeneralAsync(buf []byte, paramNames []string, paramVals [][]byte, resultVal []byte) []byte {
	return putTypeSectionOneFuncGeneralTag(buf, cFunctypeAsync, paramNames, paramVals, resultVal)
}

func putTypeSectionOneFuncGeneralTag(buf []byte, tag byte, paramNames []string, paramVals [][]byte, resultVal []byte) []byte {
	if len(paramNames) != len(paramVals) {
		panic("component: paramNames and paramVals must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) types
	body = append(body, tag)       // functype form (sync 0x40 / async 0x43)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramVals[i]...)
	}
	body = append(body, 0x00) // resultlist: single anonymous
	body = append(body, resultVal...)
	return wrapSection(buf, SectionType, body)
}

// PutTypeSectionOneFuncGeneralVoid emits a functype with the given pre-encoded
// params and NO result (the WIT `func(...)` shape — a named-results list of
// length zero). The P6 export lift uses it for a void export such as
// `wasi:http`'s `incoming-handler#handle` (docs/WIT-BRING-YOUR-OWN.md).
func PutTypeSectionOneFuncGeneralVoid(buf []byte, paramNames []string, paramVals [][]byte) []byte {
	if len(paramNames) != len(paramVals) {
		panic("component: paramNames and paramVals must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) types
	body = append(body, 0x40)      // functype form
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramVals[i]...)
	}
	body = append(body, 0x01)      // resultlist: named
	body = leb128.UlebU64(body, 0) // vec(0) results
	return wrapSection(buf, SectionType, body)
}

// PutCanonSectionLiftNoOpts emits a canon section with one
// canon-lift entry (no opts). Mirrors
// `put_canon_section_lift_no_opts`.
func PutCanonSectionLiftNoOpts(buf []byte, coreFuncIdx uint32, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // canon-lift
	body = append(body, 0x00)      // function-lift sub-tag
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 0) // no opts
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLiftAsync emits a canon section with one canon-lift
// entry carrying the `async` canonical option (0x06) — the WASI
// Preview-3 / component-model-async lift. An async-lifted export's core
// function returns void and delivers its result through `canon
// task.return` (see PutCanonTaskReturnSingle); function-return signals
// task completion. The bytes match what wasm-tools 1.240 emits for
// `(canon lift (core func N) async)` and run under
// `wasmtime -W component-model-async,component-model-async-stackful`.
// See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonSectionLiftAsync(buf []byte, coreFuncIdx uint32, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // canon-lift
	body = append(body, 0x00)      // function-lift sub-tag
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 1) // opts vec(1)
	body = append(body, 0x06)      // canonopt: async
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLiftAsyncWithMemory is PutCanonSectionLiftAsync with the
// `memory` (0x03) option appended — for an async export whose result carries
// linear-memory data (string / list). Opts vec(2) = [async 0x06, memory 0x03].
func PutCanonSectionLiftAsyncWithMemory(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // canon-lift
	body = append(body, 0x00)      // function-lift sub-tag
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 2) // opts vec(2)
	body = append(body, 0x06)      // canonopt: async
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLiftAsyncWithMemoryRealloc is PutCanonSectionLiftAsyncWithMemory
// with the `realloc` (0x04) option appended — for an async export that takes a
// string/list PARAMETER: the canonical ABI materialises the incoming bytes in
// the export's memory via its cabi_realloc before the core func runs. Opts
// vec(3) = [async 0x06, memory 0x03 <mem>, realloc 0x04 <realloc>]. Verified
// against a wasm-tools 1.240 component whose `send: async func(s: string) -> u32`
// runs under wasmtime (`send("hello") -> 5`); see docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonSectionLiftAsyncWithMemoryRealloc(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // canon-lift
	body = append(body, 0x00)      // function-lift sub-tag
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 3) // opts vec(3)
	body = append(body, 0x06)      // canonopt: async
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04) // canonopt: realloc
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonTaskReturnSingle emits a canon section with one `task.return`
// entry that lowers to a CORE function taking the result value — the
// component-model-async intrinsic an async-lifted export calls to
// deliver its single result before returning. `resultValtype` is the
// component valtype of the result (e.g. CValtypeU32). The bytes match
// wasm-tools 1.240's `(canon task.return (result <ty>))`. The produced
// core func is provided to the user core module as an import. See
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonTaskReturnSingle(buf []byte, resultValtype byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)     // vec(1)
	body = append(body, 0x09)          // canon task.return
	body = append(body, 0x00)          // result: single-value form
	body = append(body, resultValtype) // the result valtype
	body = append(body, 0x00)          // options vec(0)
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonTaskReturnStringWithMemory emits a `task.return` whose result is a
// `string` (CValtype 0x73) and which carries the `memory` (0x03) canonical
// option. An async export returning a string calls this intrinsic with the
// `(ptr, len)` of the bytes in its linear memory; task.return reads `len` bytes
// at `ptr` from `memIdx` and stages them as the lifted string result. The
// produced core func has signature `(ptr i32, len i32) -> ()`. The string/list
// counterpart of PutCanonTaskReturnSingle. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonTaskReturnStringWithMemory(buf []byte, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)      // vec(1)
	body = append(body, 0x09)           // canon task.return
	body = append(body, 0x00)           // result: single-value form
	body = append(body, cValtypeString) // the result valtype: string
	body = leb128.UlebU64(body, 1)      // options vec(1)
	body = append(body, 0x03)           // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonTaskReturnTypeIdxWithMemory is PutCanonTaskReturnStringWithMemory
// generalised to a result that is a DEFINED component type referenced by index
// (e.g. a `list<T>` emitted via PutTypeSectionOneDefined(InnerTypeList(elem))),
// rather than the inline `string` primitive. The result valtype is the type
// index encoded as an s33 sleb; the `memory` option is carried the same way
// (the core func is `(ptr, len) -> ()` for a list, whose bytes live in `memIdx`).
func PutCanonTaskReturnTypeIdxWithMemory(buf []byte, typeIdx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)                   // vec(1)
	body = append(body, 0x09)                        // canon task.return
	body = append(body, 0x00)                        // result: single-value form
	body = append(body, leb128SlebBytes(typeIdx)...) // result valtype = typeidx (s33)
	body = leb128.UlebU64(body, 1)                   // options vec(1)
	body = append(body, 0x03)                        // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonTaskReturnTypeIdx emits a `task.return` whose result is a DEFINED
// component type referenced by index, with NO canonical options. This is the
// shape an async export returning a `future<T>` / `stream<T>` uses: the result is
// the readable handle (a scalar i32), so — unlike a string/list result —
// task.return needs no `memory` option. The produced core func is
// `(readable: i32) -> ()`. Byte-verified against wasm-tools 1.240
// (`task.return (result future<u32>)` → `09 00 <typeidx> 00`). See
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func PutCanonTaskReturnTypeIdx(buf []byte, typeIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)                   // vec(1)
	body = append(body, 0x09)                        // canon task.return
	body = append(body, 0x00)                        // result: single-value form
	body = append(body, leb128SlebBytes(typeIdx)...) // result valtype = typeidx (s33)
	body = append(body, 0x00)                        // options vec(0)
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLiftWithMemory emits a canon-lift entry carrying the `memory`
// canonical-ABI option — the inverse of PutCanonSectionLowerWithMemory. A lift
// needs `memory` when the lifted function's signature carries a string / list:
// the canonical ABI reads (and, with realloc, writes) the bytes in the core
// module's linear memory. This covers a string/list RESULT export (the lift
// reads the core's returned (ptr,len)). Opts precede the typeidx for a lift
// (mirroring PutCanonSectionLiftNoOpts's field order). P6 composite exports —
// docs/WIT-BRING-YOUR-OWN.md.
func PutCanonSectionLiftWithMemory(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // canon-lift
	body = append(body, 0x00)      // function-lift sub-tag
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 1) // opts vec(1)
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutCanonSectionLiftWithMemoryRealloc emits a canon-lift entry carrying both
// `memory` and `realloc`. The realloc option is needed when the lifted function
// takes a string / list PARAMETER: the canonical ABI allocates space in the
// core module's linear memory (via cabi_realloc) to materialise the incoming
// bytes before calling the core func. P6 composite exports.
func PutCanonSectionLiftWithMemoryRealloc(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // canon-lift
	body = append(body, 0x00)      // function-lift sub-tag
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 2) // opts vec(2)
	body = append(body, 0x03)      // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04) // canonopt: realloc
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

// PutExportSectionOneFunc emits a component-level export section
// with one entry that exposes a component-level function under the
// given name. Mirrors `put_export_section_one_func`.
func PutExportSectionOneFunc(buf []byte, name string, funcIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // exportname kind = label
	body = putName(body, name)
	body = append(body, 0x01) // sort = func
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = append(body, 0x00) // no externdesc
	return wrapSection(buf, SectionExport, body)
}

// PutTypeSectionResultEmptyAndUnitFuncReturningResult emits a type
// section with two consecutive types:
//
//   - global typeidx `resultTypeidx`:        result<_, _> (no payloads)
//   - global typeidx `resultTypeidx + 1`:    func() -> result<resultTypeidx>
//
// `resultTypeidx` is the count of types declared in earlier type
// sections — i.e. the global index the first of the two new types
// lands at. The functype's single-anonymous resultlist uses an
// uleb-encoded typeidx pointing at the result type.
//
// Pair with PutCanonSectionLiftNoOpts(coreFuncIdx, resultTypeidx+1)
// to lift a core `() -> i32` to the wasi:cli/run::run shape.
func PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf []byte, resultTypeidx uint32) []byte {
	body := []byte{
		0x02,             // vec(2) type entries
		0x6a, 0x00, 0x00, // first: result<_, _>
		0x40, // second: functype form
		0x00, // vec(0) params
		0x00, // resultlist: single anonymous
	}
	body = leb128.UlebU64(body, uint64(resultTypeidx)) // valtype = typeidx of the result type
	return wrapSection(buf, SectionType, body)
}

// PutInstanceSectionOnePackagedFunc emits a component-level
// instance section that packages a single component-level function
// (by funcidx) into an instance with the given export name.
//
// Layout (body):
//
//	01    // vec(1) instances
//	01    // form 1: from-export-list
//	01    // vec(1) inline exports
//	00    // inline-export name kind = label
//	<n>   // uleb name length
//	...   // name bytes
//	01    // sort = func
//	<idx> // sortidx
//
// Pair with PutExportSectionOneInstance to expose the instance
// under a WASI interface name (e.g. "wasi:cli/run@0.2.0").
func PutInstanceSectionOnePackagedFunc(buf []byte, exportName string, funcIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) instances
	body = append(body, 0x01)      // form 1: from-export-list
	body = leb128.UlebU64(body, 1) // vec(1) inline exports
	body = append(body, 0x00)      // export name kind = label
	body = putName(body, exportName)
	body = append(body, 0x01) // sort = func
	body = leb128.UlebU64(body, uint64(funcIdx))
	return wrapSection(buf, SectionInstance, body)
}

// PutExportSectionOneInstance emits an export section with one
// label-form entry exposing a component-level instance under the
// given (typically interface-style) name. Mirrors
// PutExportSectionOneFunc but for sort = instance (0x05).
func PutExportSectionOneInstance(buf []byte, name string, instanceIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1)
	body = append(body, 0x00)      // exportname kind = label
	body = putName(body, name)
	body = append(body, 0x05) // sort = instance
	body = leb128.UlebU64(body, uint64(instanceIdx))
	body = append(body, 0x00) // no externdesc
	return wrapSection(buf, SectionExport, body)
}

// PutComponentTypeSection appends a `component-type` custom
// section carrying the given precomputed payload bytes. Mirrors
// the Lang stdlib `put_component_type_section` and the existing
// `internal/wasm/componenttype.Embed` (but at the component level
// rather than embedded in a core module).
//
// Custom sections in components share the same wire shape as in
// core wasm — section id 0x00 + uleb size + uleb name length +
// UTF-8 name + raw payload bytes.
//
// The payload bytes are deterministic per WIT world and
// independent of the surrounding component; the production driver
// ships precomputed payloads in `internal/wasm/componenttype/
// {lang,http}.bin`. This composer just wraps whatever payload the
// caller hands in.
func PutComponentTypeSection(buf []byte, payload []byte) []byte {
	body := putName(nil, "component-type")
	body = append(body, payload...)
	return wrapSection(buf, SectionCustom, body)
}
