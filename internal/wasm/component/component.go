// Package component is the Go-side counterpart of the Lang stdlib
// `std/wasm/component` module. It writes Component Model binary
// envelopes around already-encoded core wasm modules.
//
// Spec: https://github.com/WebAssembly/component-model/blob/main/design/mvp/Binary.md
//
// This package owns the encoder primitives used by the production
// driver (cmd/lang) when it composes a preview-2-native component
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
// std/wasm/component.lang's section_*).
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
