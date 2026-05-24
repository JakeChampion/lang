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

// FixupModuleFor4I32NoResult returns the bytes of the tiny core
// wasm module that, paired with TrampolineModuleFor4I32NoResult,
// closes the canon-lower / instantiation cycle.
//
// The fixup module:
//
//   - imports the canon-lowered WASI func as `("" "0")` (i32 i32
//     i32 i32) → ()
//   - imports the trampoline's funcref table as `("" "$imports")`
//   - emits an `(elem (i32.const 0) func 0)` segment that
//     installs the lowered func into table[0]
//
// Instantiating this module triggers the elem segment, after
// which the trampoline's `call_indirect` resolves to the real
// canon-lowered host func. By then the user's core module has
// already been instantiated (the trampoline indirection let it
// import a memory-less func), so the lowered func can safely
// reference the user's memory + cabi_realloc via its canon-
// lower opts.
//
// Output bytes match what wasm-tools emits for this method
// (verified by hex-dumping a canonical print-component).
func FixupModuleFor4I32NoResult() []byte {
	return FixupModuleForNI32NoResult(4)
}

// FixupModuleForNI32NoResult is the param-count-generalised
// FixupModuleFor4I32NoResult. The imported func type is
// `(i32 × nparams) -> ()`; everything else (the func + table
// imports and the elem segment that installs the lowered func
// into table[0]) is independent of the param count.
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

// TrampolineModuleFor4I32NoResult returns the bytes of a tiny
// core wasm trampoline module that exports a single function of
// type `(param i32 i32 i32 i32) -> ()` whose body is a 1-entry
// funcref-table call_indirect. The table is also exported. This
// is the shape `wasm-tools component new` uses to break the
// canon-lower / core-instantiation dependency cycle for
// list<u8>-shaped imports — the user's core module imports the
// trampoline func (no memory dependency yet), then after the
// user instance is instantiated and its memory aliased, the
// real canon-lowered func gets bound into the trampoline's
// table[0] via an elem segment in a tiny fixup module.
//
// Function-type signature (4 i32, no result) matches the
// lowered ABI for
// `wasi:io/streams::[method]output-stream.blocking-write-and-flush`:
//
//	(self: i32, ptr: i32, len: i32, ret_ptr: i32) -> ()
//
// Other lowered-method shapes will need their own trampoline
// builders; this one is fd_write-specific. Output bytes match
// what wasm-tools emits for this method (verified by hex-
// dumping a canonical print-component).
func TrampolineModuleFor4I32NoResult() []byte {
	return TrampolineModuleForNI32NoResult(4)
}

// TrampolineModuleForNI32NoResult is the param-count-generalised
// TrampolineModuleFor4I32NoResult. The exported func type is
// `(i32 × nparams) -> ()`; the body forwards all nparams to a
// 1-entry funcref-table call_indirect.
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
	body = leb128.UlebU64(body, 1)                            // vec(1) type entries
	body = append(body, 0x42)                                 // instance-type form
	body = leb128.UlebU64(body, uint64(2+len(innerTypes)))    // vec(2+N) decls

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
		body = append(body, 0x00)            // resultlist: single anonymous
		body = append(body, resultValtypes[0]) // valtype byte
	}

	// Export decl referencing the functype at inner-typeidx N.
	body = append(body, 0x04) // export decl
	body = append(body, 0x00) // exportname kind = label
	body = putName(body, exportName)
	body = append(body, 0x01)                              // externdesc kind = func
	body = leb128.UlebU64(body, uint64(len(innerTypes)))   // typeidx = N (count of inner types)

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

// InnerTypeListU8 is the defvaltype body bytes for `list<u8>` —
// the canonical-ABI byte-buffer shape used by wasi:io/streams's
// `blocking-write-and-flush(contents: list<u8>)` and similar.
//
// Encoding: `70 7d` (list form + u8 cvaltype).
var InnerTypeListU8 = []byte{0x70, CValtypeU8}

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
		0x01,                                                 // type decl
		0x40,                                                 // functype form
		0x02,                                                 // vec(2) params
		0x04, 's', 'e', 'l', 'f', 0x06,                       // param "self" valtype=typeidx 6
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x07,   // param "contents" valtype=typeidx 7
		0x00, 0x08,                                           // resultlist single anonymous, valtype = typeidx 8
	)
	// decl 10: export "[method]output-stream.blocking-write-and-flush" (func 9)
	body = append(body, 0x04, 0x00, 0x2e) // export, kind=label, name-len 46
	body = append(body, "[method]output-stream.blocking-write-and-flush"...)
	body = append(body, 0x01, 0x09) // externdesc func, typeidx 9
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
	body = append(body, ExportSubResourceDecl("input-stream")...)        // inner 0
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...)      // inner 1
	body = append(body, ExportTypeEqDecl("error", 1)...)                 // inner 2
	body = append(body, 0x01, 0x69, 0x02)                                // inner 3: own<2>
	body = append(body, 0x01)                                            // inner 4: variant
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})...)
	body = append(body, ExportTypeEqDecl("stream-error", 4)...)          // inner 5
	body = append(body, 0x01)                                            // inner 6: borrow<0>
	body = append(body, InnerTypeBorrow(0)...)
	body = append(body, 0x01)                                            // inner 7: list<u8>
	body = append(body, InnerTypeListU8...)
	body = append(body, 0x01)                                            // inner 8: result<ok=7, err=5>
	body = append(body, InnerTypeResultOkErr(7, 5)...)
	// inner 9: func(self: borrow<input-stream> typeidx 6, len: u64) -> typeidx 8
	body = append(body,
		0x01,                          // type decl
		0x40,                          // functype form
		0x02,                          // vec(2) params
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
	body = append(body, 0x00)         // importname kind = label
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
	body = append(body, 0x01)         // sort = func
	body = append(body, 0x00)         // target: from-instance-export
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
	body = append(body, 0x03)         // sort = type
	body = append(body, 0x00)         // target: from-instance-export
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
	body = append(body, 0x01)         // canon-lower
	body = append(body, 0x00)         // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 0) // no opts
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
	body = leb128.UlebU64(body, 1)             // vec(1) canons
	body = append(body, 0x01)                  // canon-lower
	body = append(body, 0x00)                  // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 1)             // opts vec(1)
	body = append(body, 0x03)                  // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
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
	body = leb128.UlebU64(body, 1)                       // vec(1) canons
	body = append(body, 0x01)                            // canon-lower
	body = append(body, 0x00)                            // function-lower sub-tag
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 2)                       // opts vec(2)
	body = append(body, 0x03)                            // canonopt: memory
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04)                            // canonopt: realloc
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
	body = append(body, 0x01)         // form: from-exports
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
	body = append(body, 0x00)         // form: instantiate
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

// PutTypeSectionOneFunc emits a component-level type section
// containing one functype with N params + a single anonymous
// result. Mirrors `put_type_section_one_func`.
func PutTypeSectionOneFunc(buf []byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1) // vec(1) types
	body = append(body, 0x40)      // functype form
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	body = append(body, 0x00) // resultlist: single anonymous
	body = append(body, resultValtype)
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
		0x40,             // second: functype form
		0x00,             // vec(0) params
		0x00,             // resultlist: single anonymous
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
