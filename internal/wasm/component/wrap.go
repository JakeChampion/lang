package component

// WasiImport describes one preview-2 WASI interface import the
// core module expects. The Wrap* helpers translate each one into
// the type / import / alias / canon-lower / core-instance
// pipeline.
type WasiImport struct {
	// InterfaceName is the component-level import name, e.g.
	// "wasi:cli/exit@0.2.0".
	InterfaceName string

	// FuncName is the function name within the interface, e.g.
	// "exit". The wrap helper threads this all the way through:
	//   - It's the alias target name (instance.export).
	//   - It's the core-instance export name (so the core module
	//     can resolve `(import "<CoreImportModule>" "<FuncName>")`).
	FuncName string

	// ParamNames are the component-level parameter names. Length
	// must equal ParamValtypes.
	ParamNames []string

	// ParamValtypes are component-level valtype bytes for each
	// parameter. Each byte is either a primitive cvaltype constant
	// (CValtype* in 0x73..0x7f) or an inner-scope typeidx into
	// InnerTypes (byte 0x00..0x72, where 0x00 references
	// InnerTypes[0]).
	ParamValtypes []byte

	// CoreImportModule is the module name the core wasm module
	// uses in its `(import "X" "FuncName" ...)` declaration. The
	// component's instantiate-with-args section will pass the
	// packaged core instance under this name.
	CoreImportModule string

	// InnerTypes are optional defvaltype encodings declared inside
	// the instance type before the function declaration. Used when
	// the imported function's signature references a non-primitive
	// type (e.g. `result<_, _>` for wasi:cli/exit::exit). Each
	// entry is the raw defvaltype body (form byte + payload);
	// InnerTypeResultEmpty packages the empty-arm result type.
	// nil/empty preserves the original behaviour
	// (primitive-only params).
	InnerTypes [][]byte

	// ResultValtypes is the optional single-anonymous-result
	// component-level valtype for the imported function. nil/empty
	// means void (`func(...) -> ()`); a one-byte slice means
	// `func(...) -> <byte>`. Used by imports like
	// wasi:random/random::get-random-u64 that return a scalar.
	// Multi-result functions aren't supported yet.
	ResultValtypes []byte

	// RawInstanceTypeBody is an escape hatch for instance-type
	// encodings the structured fields (FuncName, ParamNames,
	// ParamValtypes, InnerTypes, ResultValtypes) can't express.
	// When non-nil it's used verbatim as the type-section body
	// bytes — vec(1) types + instance type form + decls — and
	// the structured fields above are ignored for type-section
	// emission.
	//
	// The other fields (InterfaceName, CoreImportModule, FuncName)
	// still drive the import / alias / canon-lower / core-
	// instance sections. Use this for interfaces that need
	// exported type aliases inside the instance (e.g.
	// wasi:clocks/wall-clock exports `datetime` then references
	// it from `now`), resource type declarations, or
	// method-call shapes (self-handle as first param).
	RawInstanceTypeBody []byte
}

// WrapWasiImported produces a Component-Model binary that wraps
// `coreBytes` (a core wasm module that imports each WASI host
// function listed in `imports`) into a preview-2-native component.
//
// The component:
//
//  1. Declares an instance type per WASI import describing its
//     interface.
//  2. Imports each interface under its WASI name (e.g.
//     "wasi:cli/exit@0.2.0").
//  3. Aliases each interface's function as a component-level func.
//  4. Lowers each component-level func to a core func.
//  5. Packages each lowered core func into a single-export core
//     instance.
//  6. Embeds the core module.
//  7. Instantiates the core module, passing each packaged core
//     instance under its CoreImportModule name.
//
// This is the Go-side counterpart of
// `build_wasi_multi_imported_component` in std/wasm/component.fern.
// The two are kept byte-for-byte equivalent (modulo Go's vs Lang's
// uleb encoding edge cases — both should round-trip identical bytes
// through `wasm-tools print`).
//
// Limitations of the current shape:
//   - Functions are restricted to no-result signatures (the WASI
//     `exit`-style shape). Real WASI has many returning functions;
//     extending this is a future slice.
//   - Only scalar parameters / no canonical-ABI string-list-record
//     lowering opts.
//   - No component-level export — the wrapped component runs its
//     side effects via the imported WASI funcs and exits.
func WrapWasiImported(coreBytes []byte, imports []WasiImport) []byte {
	buf := PutComponentHeader(nil)

	// For each WASI import: type, import, alias, lower, core-instance.
	for i, imp := range imports {
		if imp.RawInstanceTypeBody != nil {
			buf = PutTypeSectionRawBody(buf, imp.RawInstanceTypeBody)
		} else {
			buf = PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf, imp.InnerTypes, imp.FuncName, imp.ParamNames, imp.ParamValtypes, imp.ResultValtypes)
		}
		buf = PutImportSectionOneInstance(buf, imp.InterfaceName, uint32(i))
		buf = PutAliasSectionInstanceExportFunc(buf, uint32(i), imp.FuncName)
		buf = PutCanonSectionLowerNoOpts(buf, uint32(i))
		buf = PutCoreInstanceSectionFromOneFuncExport(buf, imp.FuncName, uint32(i))
	}

	// Embed the core module + instantiate it with N instance args.
	buf = PutCoreModuleSection(buf, coreBytes)
	argNames := make([]string, len(imports))
	instanceIdxs := make([]uint32, len(imports))
	for i, imp := range imports {
		argNames[i] = imp.CoreImportModule
		instanceIdxs[i] = uint32(i)
	}
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, argNames, instanceIdxs)
	return buf
}

// BuildLiftedExportComponent is the Go-side counterpart of the
// Lang stdlib's `build_lifted_export_component_with_params`. Wraps
// a core module so one of its exported functions is callable as a
// component-level function with the given parameter list + single
// anonymous result valtype.
//
// The core function must have a core signature compatible with the
// canonical-ABI lowering of `(paramValtypes) -> resultValtype`. For
// scalar valtypes that's a one-to-one mapping (i32/u32 → i32,
// i64/u64 → i64, f32 → f32, f64 → f64).
//
// Recipe (matches the Lang helper):
//
//  1. core-module:    embed the core module
//  2. core-instance:  instantiate (no args)
//  3. alias:          alias the named core export as a core-func
//  4. type:           declare the component-level functype
//  5. canon:          lift core-func 0 → component-func 0 (no opts)
//  6. export:         expose component-func 0 as exportName
//
// This is the simplest preview-2 component shape that wasmtime
// can `--invoke`. The byte-equivalence test pins the output
// against the Lang version.
func BuildLiftedExportComponent(coreBytes []byte, coreExportName, exportName string, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, coreExportName)
	buf = PutTypeSectionOneFunc(buf, paramNames, paramValtypes, resultValtype)
	buf = PutCanonSectionLiftNoOpts(buf, 0, 0)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildWasiCliRunComponent wraps a core module that exports a
// `() -> i32`-typed function as a preview-2 component implementing
// the `wasi:cli/run@0.2.0` world. Wasmtime can run such a
// component directly with `wasmtime run prog.wasm` (no `--invoke`
// flag) — the host treats the lifted return value (i32 → result<_,
// _>: 0 = ok, non-zero = err) as the process exit signal.
//
// Recipe:
//
//  1. core-module: embed the core module.
//  2. core-instance: instantiate it (no args).
//  3. alias: surface the named core export as core-func 0.
//  4. type: declare result<_, _> at type 0 + func() -> result<0>
//     at type 1.
//  5. canon: lift core-func 0 → component-func 0 of type 1 (no
//     opts).
//  6. instance: package component-func 0 into an instance with
//     the inline export name "run".
//  7. export: expose instance 0 as "wasi:cli/run@0.2.0".
//
// The core export's signature must be `() -> i32` for the canon
// lift to succeed (the wasmtime linker will reject any other
// shape).
func BuildWasiCliRunComponent(coreBytes []byte, coreExportName string) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, coreExportName)
	buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf, 0)
	buf = PutCanonSectionLiftNoOpts(buf, 0, 1)
	buf = PutInstanceSectionOnePackagedFunc(buf, "run", 0)
	buf = PutExportSectionOneInstance(buf, "wasi:cli/run@0.2.0", 0)
	return buf
}

// WrapWasiImportedAsCliRun is the wasi:cli/run-exporting sibling
// of WrapWasiImportedWithExport: same import-wiring pipeline, but
// the core export gets packaged into a `wasi:cli/run@0.2.0`
// instance (matching BuildWasiCliRunComponent's shape) instead of
// being lifted as a top-level component-level function. The
// produced component runs under plain `wasmtime run prog.wasm`
// (no `--invoke`) and still has its WASI imports satisfied by
// the host.
//
// The core function `coreExportName` must have signature `() ->
// i32` for the canon lift to wasi:cli/run::run's `() -> result`
// to succeed.
//
// Index assignment after N imports:
//
//   - Component types: 0..N-1 are per-import instance types, N is
//     the result type, N+1 is the run functype.
//   - Component funcs: 0..N-1 are per-import aliased funcs, N is
//     the lifted core export.
//   - Component instances: 0..N-1 are the imported WASI instances,
//     N is the packaged instance carrying "run".
//   - Core funcs: 0..N-1 are per-import canon-lowers, N is the
//     aliased core export.
//   - Core instances: 0..N-1 are per-import single-export packaging
//     instances, N is the instantiation of the embedded core module.
func WrapWasiImportedAsCliRun(coreBytes []byte, imports []WasiImport, coreExportName string) []byte {
	buf := PutComponentHeader(nil)

	n := uint32(len(imports))
	for i, imp := range imports {
		if imp.RawInstanceTypeBody != nil {
			buf = PutTypeSectionRawBody(buf, imp.RawInstanceTypeBody)
		} else {
			buf = PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf, imp.InnerTypes, imp.FuncName, imp.ParamNames, imp.ParamValtypes, imp.ResultValtypes)
		}
		buf = PutImportSectionOneInstance(buf, imp.InterfaceName, uint32(i))
		buf = PutAliasSectionInstanceExportFunc(buf, uint32(i), imp.FuncName)
		buf = PutCanonSectionLowerNoOpts(buf, uint32(i))
		buf = PutCoreInstanceSectionFromOneFuncExport(buf, imp.FuncName, uint32(i))
	}

	buf = PutCoreModuleSection(buf, coreBytes)
	argNames := make([]string, len(imports))
	instanceIdxs := make([]uint32, len(imports))
	for i, imp := range imports {
		argNames[i] = imp.CoreImportModule
		instanceIdxs[i] = uint32(i)
	}
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, argNames, instanceIdxs)

	buf = PutAliasSectionCoreExportFunc(buf, n, coreExportName)
	buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf, n)
	buf = PutCanonSectionLiftNoOpts(buf, n, n+1)
	buf = PutInstanceSectionOnePackagedFunc(buf, "run", n)
	buf = PutExportSectionOneInstance(buf, "wasi:cli/run@0.2.0", n)
	return buf
}

// WrapWasiPrintComponent wraps a core module that imports
// `wasi:cli/stdout::get-stdout` and
// `wasi:io/streams::[method]output-stream.blocking-write-and-flush`
// into a preview-2 component. The user's core module must:
//
//   - import `wasi:cli/stdout@0.2.0::get-stdout` as `() -> i32`
//   - import `wasi:io/streams@0.2.0::[method]output-stream.blocking-write-and-flush`
//     as `(self: i32, ptr: i32, len: i32, ret_ptr: i32) -> ()`
//   - export `memory` and `cabi_realloc`
//
// The wrap uses the canonical wasm-tools-equivalent shape:
// three imported interfaces (wasi:io/error, wasi:io/streams,
// wasi:cli/stdout) with shared resource types via outer aliases,
// the user's core module + a trampoline + a fixup module to
// break the canon-lower / instantiation cycle for the
// list<u8>-shaped blocking-write-and-flush import.
//
// This composer returns just the component bytes. Pair with a
// lifted-export wrap (BuildWasiCliRunComponent etc.) for a
// runnable end product if desired.
func WrapWasiPrintComponent(coreBytes []byte) []byte {
	return wrapWasiStreamWriteComponent(coreBytes, "wasi:cli/stdout@0.2.0", "get-stdout")
}

// WrapWasiEprintComponent is the wasi:cli/stderr sibling of
// WrapWasiPrintComponent — wraps a core module that imports
// `wasi:cli/stderr::get-stderr` + the blocking-write-and-flush
// method into a preview-2 component. Same trampoline / fixup
// shape; only the stdio interface differs.
func WrapWasiEprintComponent(coreBytes []byte) []byte {
	return wrapWasiStreamWriteComponent(coreBytes, "wasi:cli/stderr@0.2.0", "get-stderr")
}

// wrapWasiStreamWriteComponent is the shared implementation for
// WrapWasiPrintComponent (stdout) and WrapWasiEprintComponent
// (stderr). `cliInterface` / `getFuncName` select the stdio
// interface; the wasi:io/error + wasi:io/streams imports and the
// trampoline / fixup wiring are identical for both.
func wrapWasiStreamWriteComponent(coreBytes []byte, cliInterface, getFuncName string) []byte {
	buf := PutComponentHeader(nil)

	// Type 0: wasi:io/error instance type. Import as instance 0.
	buf = PutTypeSectionRawBody(buf, WasiIoErrorInstanceTypeBody())
	buf = PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	// Top-level alias of `error` → type 1.
	buf = PutAliasSectionInstanceExportType(buf, 0, "error")

	// Type 2: wasi:io/streams instance type (references type 1).
	// Import as instance 1.
	buf = PutTypeSectionRawBody(buf, WasiIoStreamsInstanceTypeBody(1))
	buf = PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	// Top-level alias of `output-stream` → type 3.
	buf = PutAliasSectionInstanceExportType(buf, 1, "output-stream")

	// Type 4: wasi:cli/std{out,err} instance type (references
	// type 3). Import as instance 2.
	if getFuncName == "get-stderr" {
		buf = PutTypeSectionRawBody(buf, WasiCliStderrInstanceTypeBody(3))
	} else {
		buf = PutTypeSectionRawBody(buf, WasiCliStdoutInstanceTypeBody(3))
	}
	buf = PutImportSectionOneInstance(buf, cliInterface, 4)

	// Core modules: user (0), trampoline (1), fixup (2).
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreModuleSection(buf, TrampolineModuleFor4I32NoResult())
	buf = PutCoreModuleSection(buf, FixupModuleFor4I32NoResult())

	// Instantiate trampoline (no args) → core instance 0.
	buf = PutCoreInstanceSectionInstantiate(buf, 1)

	// Alias get-std{out,err} from instance 2 → component func 0,
	// canon-lower it (no opts — `() -> handle` doesn't need
	// memory) → core func 0, wrap as core instance 1.
	buf = PutAliasSectionInstanceExportFunc(buf, 2, getFuncName)
	buf = PutCanonSectionLowerNoOpts(buf, 0)
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, getFuncName, 0)

	// Alias "0" func from trampoline (core instance 0) → core
	// func 1. Wrap as core instance 2 under the method name.
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "[method]output-stream.blocking-write-and-flush", 1)

	// Instantiate user core module with std{out,err}=instance 1,
	// streams=instance 2 (the trampoline wrapper) → core
	// instance 3.
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0,
		[]string{cliInterface, "wasi:io/streams@0.2.0"},
		[]uint32{1, 2})

	// Alias memory from the user core instance + the trampoline's
	// table. We don't alias cabi_realloc — our canon-lower uses
	// memory-only opts and the result-return area lives in the
	// bump heap (allocated via __fern_alloc), not via realloc.
	// Skipping the cabi_realloc alias also means wasmbin doesn't
	// need to export it on the print-only-imports path.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 3, "memory")
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")

	// Alias blocking-write-and-flush from streams instance →
	// component func 1.
	buf = PutAliasSectionInstanceExportFunc(buf, 1, "[method]output-stream.blocking-write-and-flush")

	// Canon-lower the method with memory(0) → core func 2.
	buf = PutCanonSectionLowerWithMemory(buf, 1, 0)

	// Build the fixup arg instance — packages the trampoline's
	// table + the lowered func → core instance 4.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 2},
	})

	// Instantiate the fixup module with that arg → core
	// instance 5. Instantiation triggers the elem segment that
	// installs the lowered func into the trampoline's table[0].
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2,
		[]string{""}, []uint32{4})

	return buf
}

// WrapWasiReadComponent wraps a core module that imports
// `wasi:cli/stdin::get-stdin` and
// `wasi:io/streams::[method]input-stream.blocking-read` into a
// preview-2 component — the read-side counterpart of
// WrapWasiPrintComponent. The user's core module must:
//
//   - import `wasi:cli/stdin@0.2.0::get-stdin` as `() -> i32`
//   - import `wasi:io/streams@0.2.0::[method]input-stream.blocking-read`
//     as `(self: i32, len: i64, ret_ptr: i32) -> ()`
//   - export `memory` and `cabi_realloc`
//
// The shape mirrors the write wrap (three imported interfaces —
// wasi:io/error, wasi:io/streams, wasi:cli/stdin — with shared
// resource types via outer aliases, plus trampoline + fixup to
// break the canon-lower / instantiation cycle), with two
// read-specific differences:
//
//   - the lowered blocking-read ABI is `(i32, i64, i32) -> ()`
//     (the mixed-valtype trampoline), and
//   - blocking-read returns `result<list<u8>, stream-error>`, so
//     its canon-lower needs `realloc` (to allocate the returned
//     list in the user's memory) in addition to `memory`. The
//     user's `cabi_realloc` export is aliased for that.
func WrapWasiReadComponent(coreBytes []byte) []byte {
	readParams := []byte{0x7f, 0x7e, 0x7f} // (self: i32, len: i64, ret_ptr: i32)
	buf := PutComponentHeader(nil)

	// Type 0: wasi:io/error instance type. Import as instance 0.
	buf = PutTypeSectionRawBody(buf, WasiIoErrorInstanceTypeBody())
	buf = PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	// Top-level alias of `error` → type 1.
	buf = PutAliasSectionInstanceExportType(buf, 0, "error")

	// Type 2: read-side wasi:io/streams instance type (references
	// type 1). Import as instance 1.
	buf = PutTypeSectionRawBody(buf, WasiIoStreamsReadInstanceTypeBody(1))
	buf = PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	// Top-level alias of `input-stream` → type 3.
	buf = PutAliasSectionInstanceExportType(buf, 1, "input-stream")

	// Type 4: wasi:cli/stdin instance type (references type 3).
	// Import as instance 2.
	buf = PutTypeSectionRawBody(buf, WasiCliStdinInstanceTypeBody(3))
	buf = PutImportSectionOneInstance(buf, "wasi:cli/stdin@0.2.0", 4)

	// Core modules: user (0), trampoline (1), fixup (2). The
	// trampoline / fixup carry the mixed (i32, i64, i32) read ABI.
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsNoResult(readParams))
	buf = PutCoreModuleSection(buf, FixupModuleForParamsNoResult(readParams))

	// Instantiate trampoline (no args) → core instance 0.
	buf = PutCoreInstanceSectionInstantiate(buf, 1)

	// Alias get-stdin from instance 2 → component func 0,
	// canon-lower it (no opts — `() -> handle`) → core func 0,
	// wrap as core instance 1.
	buf = PutAliasSectionInstanceExportFunc(buf, 2, "get-stdin")
	buf = PutCanonSectionLowerNoOpts(buf, 0)
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "get-stdin", 0)

	// Alias "0" func from trampoline (core instance 0) → core
	// func 1. Wrap as core instance 2 under the method name.
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "[method]input-stream.blocking-read", 1)

	// Instantiate user core module with stdin=instance 1,
	// streams=instance 2 (the trampoline wrapper) → core
	// instance 3.
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0,
		[]string{"wasi:cli/stdin@0.2.0", "wasi:io/streams@0.2.0"},
		[]uint32{1, 2})

	// Alias memory + cabi_realloc from the user core instance and
	// the trampoline's table. Unlike the write wrap, the read
	// canon-lower needs realloc: blocking-read returns a
	// host-allocated list<u8>, so the canonical ABI calls
	// cabi_realloc to place those bytes in the user's memory.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 3, "memory")
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 3, "cabi_realloc") // core func 2
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")

	// Alias blocking-read from streams instance → component func 1.
	buf = PutAliasSectionInstanceExportFunc(buf, 1, "[method]input-stream.blocking-read")

	// Canon-lower the method with memory(0) + realloc(core func 2)
	// → core func 3.
	buf = PutCanonSectionLowerWithMemoryRealloc(buf, 1, 0, 2)

	// Build the fixup arg instance — packages the trampoline's
	// table + the lowered func (core func 3) → core instance 4.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 3},
	})

	// Instantiate the fixup module with that arg → core
	// instance 5. Triggers the elem segment that installs the
	// lowered func into the trampoline's table[0].
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2,
		[]string{""}, []uint32{4})

	return buf
}

// WrapWasiPrintAsCliRun extends WrapWasiPrintComponent with a
// `wasi:cli/run@0.2.0` export so the produced component runs
// under plain `wasmtime run prog.wasm` (no `--invoke`). The
// user core module must additionally export `coreExportName`
// (typically `_lang_run`) with signature `() -> i32` —
// wasmtime treats the lifted i32 as result<_, _>: 0 = ok,
// non-zero = err.
//
// After WrapWasiPrintComponent finishes, the index spaces are:
//
//   - Component types: 5 (3 instance types + 2 outer-aliased
//     resource types).
//   - Component funcs: 2 (get-stdout alias, write-and-flush
//     alias).
//   - Component instances: 3 (the 3 imported WASI interfaces).
//   - Core funcs: 3 (lower get-stdout, alias trampoline, lower
//     write-and-flush).
//   - Core instances: 6 (trampoline, get-stdout wrapper,
//     write-and-flush wrapper, user, fixup arg, fixup).
//
// The cli-run tail then adds:
//   - alias core export `_lang_run` from user core instance
//     (index 3) → core func 3.
//   - type 5 = result<_, _>, type 6 = func() -> typeidx 5.
//   - canon lift core func 3 → component func 2 of type 6.
//   - packaged instance with "run" → component instance 3.
//   - export instance 3 as wasi:cli/run@0.2.0.
func WrapWasiPrintAsCliRun(coreBytes []byte, coreExportName string) []byte {
	buf := WrapWasiPrintComponent(coreBytes)
	return appendCliRunExport(buf, coreExportName)
}

// WrapWasiEprintAsCliRun is the wasi:cli/stderr sibling of
// WrapWasiPrintAsCliRun. Same cli-run tail; only the wrapped
// stdio interface differs (stderr instead of stdout).
func WrapWasiEprintAsCliRun(coreBytes []byte, coreExportName string) []byte {
	buf := WrapWasiEprintComponent(coreBytes)
	return appendCliRunExport(buf, coreExportName)
}

// appendCliRunExport adds the wasi:cli/run@0.2.0 export tail to a
// component produced by wrapWasiStreamWriteComponent. Both the
// stdout and stderr wraps leave the index spaces in the same
// state (component types 5, component funcs 2, component
// instances 3, core funcs 3, core instances 6), so the tail is
// shared. The run export is core func 3 (the first func after the
// three lowers).
func appendCliRunExport(buf []byte, coreExportName string) []byte {
	return appendCliRunExportAt(buf, coreExportName, 3)
}

// appendCliRunExportAt is the core-func-index-parameterised
// appendCliRunExport. `runCoreFuncIdx` is the index the run
// export's core func lands at — i.e. the count of core funcs the
// wrap already defined (3 for the write/print wrap, 4 for the read
// wrap, which has an extra cabi_realloc alias). The component-level
// indices (type 5/6, component func 2, instance 3) are the same
// across these wraps, so only the lifted core func index varies.
func appendCliRunExportAt(buf []byte, coreExportName string, runCoreFuncIdx uint32) []byte {
	buf = PutAliasSectionCoreExportFunc(buf, 3, coreExportName)
	buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf, 5)
	buf = PutCanonSectionLiftNoOpts(buf, runCoreFuncIdx, 6)
	buf = PutInstanceSectionOnePackagedFunc(buf, "run", 2)
	buf = PutExportSectionOneInstance(buf, "wasi:cli/run@0.2.0", 3)
	return buf
}

// WrapWasiReadAsCliRun extends WrapWasiReadComponent with a
// `wasi:cli/run@0.2.0` export so the produced component runs under
// plain `wasmtime run prog.wasm`. Like WrapWasiPrintAsCliRun but
// for the read wrap, whose index spaces match the write wrap
// except for one extra core func (the cabi_realloc alias) — so the
// run export lifts core func 4 instead of 3.
func WrapWasiReadAsCliRun(coreBytes []byte, coreExportName string) []byte {
	buf := WrapWasiReadComponent(coreBytes)
	return appendCliRunExportAt(buf, coreExportName, 4)
}

// WrapWasiWallClockComponent wraps a core module that imports
// `wasi:clocks/wall-clock@0.2.0::now` (lowered to `(out_ptr i32)
// -> ()` for the indirect datetime return) into a preview-2
// component. The user's core module must export `memory`.
//
// Simpler than the print wrap — wall-clock has no resource
// dependencies, so there's a single imported interface (no
// wasi:io/error / wasi:io/streams). But `now`'s canon-lower
// still needs the user instance's memory (datetime is returned
// via an out-pointer), so the 1-i32 trampoline / fixup pattern
// breaks the canon-lower / instantiation cycle.
//
// Index assignment:
//
//   - Component type 0: wall-clock instance type.
//   - Component instance 0: the imported wall-clock.
//   - Component func 0: aliased `now`.
//   - Core modules: user (0), trampoline-1i32 (1), fixup-1i32 (2).
//   - Core instance 0: trampoline. Core func 0: its "0" export.
//   - Core instance 1: packages core func 0 as "now".
//   - Core instance 2: the user module, instantiated with
//     wasi:clocks/wall-clock = core instance 1.
//   - Core memory 0 / table 0: aliased from instances 2 / 0.
//   - Core func 1: canon-lower of `now` with memory(0).
//   - Core instance 3: fixup arg (table + func 1).
//   - Core instance 4: the fixup module.
func WrapWasiWallClockComponent(coreBytes []byte) []byte {
	buf := PutComponentHeader(nil)

	// Type 0: wall-clock instance type. Import as instance 0.
	buf = PutTypeSectionRawBody(buf, WasiClocksWallClockInstanceTypeBody())
	buf = PutImportSectionOneInstance(buf, "wasi:clocks/wall-clock@0.2.0", 0)

	// Core modules: user (0), trampoline (1), fixup (2).
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreModuleSection(buf, TrampolineModuleForNI32NoResult(1))
	buf = PutCoreModuleSection(buf, FixupModuleForNI32NoResult(1))

	// Instantiate trampoline → core instance 0.
	buf = PutCoreInstanceSectionInstantiate(buf, 1)

	// Alias trampoline "0" → core func 0; wrap as core instance 1
	// under "now".
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "now", 0)

	// Instantiate user with wasi:clocks/wall-clock = instance 1
	// → core instance 2.
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0,
		[]string{"wasi:clocks/wall-clock@0.2.0"}, []uint32{1})

	// Alias user memory + trampoline table.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "memory")
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")

	// Alias `now` from the wall-clock import → component func 0,
	// canon-lower it with memory(0) → core func 1.
	buf = PutAliasSectionInstanceExportFunc(buf, 0, "now")
	buf = PutCanonSectionLowerWithMemory(buf, 0, 0)

	// Fixup arg instance (table + lowered func) → core instance 3,
	// then instantiate the fixup module → core instance 4.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 1},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2,
		[]string{""}, []uint32{3})
	return buf
}

// WrapWasiWallClockAsCliRun extends WrapWasiWallClockComponent
// with a wasi:cli/run@0.2.0 export. After the wall-clock wrap,
// the index spaces are: component types 1, component funcs 1,
// component instances 1, core funcs 2, core instances 5. The
// cli-run tail aliases the user's `coreExportName` (core
// instance 2 — the user module) as core func 2, lifts it, and
// packages + exports a run instance.
func WrapWasiWallClockAsCliRun(coreBytes []byte, coreExportName string) []byte {
	buf := WrapWasiWallClockComponent(coreBytes)
	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName)
	buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf, 1)
	buf = PutCanonSectionLiftNoOpts(buf, 2, 2)
	buf = PutInstanceSectionOnePackagedFunc(buf, "run", 1)
	buf = PutExportSectionOneInstance(buf, "wasi:cli/run@0.2.0", 1)
	return buf
}

// WrapWasiArgsComponent wraps a core module that imports
// `wasi:cli/environment@0.2.0::get-arguments` (lowered to
// `(retptr: i32) -> ()` for the indirect list<string> return)
// into a preview-2 component. The user's core module must export
// `memory` and `cabi_realloc`.
//
// Same single-interface / 1-i32-trampoline shape as the wall-clock
// wrap, with one difference: get-arguments returns a list<string>,
// a variable-size canonical-ABI value, so its canon-lower carries
// realloc as well as memory (the host allocates the list + each
// string's bytes in the user's memory through cabi_realloc).
//
// Index assignment:
//
//   - Component type 0: environment instance type.
//   - Component instance 0: the imported environment.
//   - Component func 0: aliased `get-arguments`.
//   - Core modules: user (0), trampoline-1i32 (1), fixup-1i32 (2).
//   - Core instance 0: trampoline. Core func 0: its "0" export.
//   - Core instance 1: packages core func 0 as "get-arguments".
//   - Core instance 2: the user module, instantiated with
//     wasi:cli/environment = core instance 1.
//   - Core memory 0: aliased from instance 2. Core func 1:
//     cabi_realloc aliased from instance 2. Core table 0: aliased
//     from instance 0.
//   - Core func 2: canon-lower of `get-arguments` with
//     memory(0) + realloc(1).
//   - Core instance 3: fixup arg (table + func 2).
//   - Core instance 4: the fixup module.
func WrapWasiArgsComponent(coreBytes []byte) []byte {
	buf := PutComponentHeader(nil)

	// Type 0: environment instance type. Import as instance 0.
	buf = PutTypeSectionRawBody(buf, WasiCliEnvironmentArgsInstanceTypeBody())
	buf = PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)

	// Core modules: user (0), trampoline (1), fixup (2).
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreModuleSection(buf, TrampolineModuleForNI32NoResult(1))
	buf = PutCoreModuleSection(buf, FixupModuleForNI32NoResult(1))

	// Instantiate trampoline → core instance 0.
	buf = PutCoreInstanceSectionInstantiate(buf, 1)

	// Alias trampoline "0" → core func 0; wrap as core instance 1
	// under "get-arguments".
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "get-arguments", 0)

	// Instantiate user with wasi:cli/environment = instance 1
	// → core instance 2.
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0,
		[]string{"wasi:cli/environment@0.2.0"}, []uint32{1})

	// Alias user memory + cabi_realloc (the list<string> return is
	// allocated through it) + trampoline table.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "memory")
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 2, "cabi_realloc") // core func 1
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")

	// Alias `get-arguments` from the import → component func 0,
	// canon-lower it with memory(0) + realloc(core func 1) → core
	// func 2.
	buf = PutAliasSectionInstanceExportFunc(buf, 0, "get-arguments")
	buf = PutCanonSectionLowerWithMemoryRealloc(buf, 0, 0, 1)

	// Fixup arg instance (table + lowered func 2) → core instance 3,
	// then instantiate the fixup module → core instance 4.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 2},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2,
		[]string{""}, []uint32{3})
	return buf
}

// WrapWasiArgsAsCliRun extends WrapWasiArgsComponent with a
// wasi:cli/run@0.2.0 export. After the args wrap, the index spaces
// are: component types 1, component funcs 1, component instances 1,
// core funcs 3, core instances 5. The cli-run tail aliases the
// user's `coreExportName` (core instance 2) as core func 3, lifts
// it, and packages + exports a run instance. (One more core func
// than the wall-clock wrap, which has no cabi_realloc alias.)
func WrapWasiArgsAsCliRun(coreBytes []byte, coreExportName string) []byte {
	buf := WrapWasiArgsComponent(coreBytes)
	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName)
	buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf, 1)
	buf = PutCanonSectionLiftNoOpts(buf, 3, 2)
	buf = PutInstanceSectionOnePackagedFunc(buf, "run", 1)
	buf = PutExportSectionOneInstance(buf, "wasi:cli/run@0.2.0", 1)
	return buf
}

// WrapWasiImportedWithExport composes WrapWasiImported's import
// wiring with BuildLiftedExportComponent's export wiring. The
// resulting component:
//
//   - Imports each WASI interface listed in `imports` (preview-2
//     shape).
//   - Embeds the core module + instantiates it with the lowered
//     WASI funcs provided as instance arguments.
//   - Lifts a named core export (`coreExportName`) as a
//     component-level function with the given parameter list +
//     single anonymous result valtype, exported as `exportName`.
//
// Index assignment after N imports:
//
//   - Component types: 0..N-1 are the per-import instance types,
//     index N is the export's functype.
//   - Component funcs: 0..N-1 are the per-import aliased funcs,
//     index N is the lifted core export.
//   - Core funcs: 0..N-1 are the per-import canon-lowers, index N
//     is the aliased core export.
//   - Core instances: 0..N-1 are the per-import single-export
//     packaging instances, index N is the instantiation of the
//     embedded core module.
//
// Limits inherited from the WASI side: scalar params only, no
// canonical-ABI string / list / record lowering opts, no result on
// imported WASI funcs.
func WrapWasiImportedWithExport(
	coreBytes []byte,
	imports []WasiImport,
	coreExportName, exportName string,
	paramNames []string,
	paramValtypes []byte,
	resultValtype byte,
) []byte {
	buf := PutComponentHeader(nil)

	n := uint32(len(imports))
	for i, imp := range imports {
		if imp.RawInstanceTypeBody != nil {
			buf = PutTypeSectionRawBody(buf, imp.RawInstanceTypeBody)
		} else {
			buf = PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf, imp.InnerTypes, imp.FuncName, imp.ParamNames, imp.ParamValtypes, imp.ResultValtypes)
		}
		buf = PutImportSectionOneInstance(buf, imp.InterfaceName, uint32(i))
		buf = PutAliasSectionInstanceExportFunc(buf, uint32(i), imp.FuncName)
		buf = PutCanonSectionLowerNoOpts(buf, uint32(i))
		buf = PutCoreInstanceSectionFromOneFuncExport(buf, imp.FuncName, uint32(i))
	}

	buf = PutCoreModuleSection(buf, coreBytes)
	argNames := make([]string, len(imports))
	instanceIdxs := make([]uint32, len(imports))
	for i, imp := range imports {
		argNames[i] = imp.CoreImportModule
		instanceIdxs[i] = uint32(i)
	}
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, argNames, instanceIdxs)

	buf = PutAliasSectionCoreExportFunc(buf, n, coreExportName)
	buf = PutTypeSectionOneFunc(buf, paramNames, paramValtypes, resultValtype)
	buf = PutCanonSectionLiftNoOpts(buf, n, n)
	buf = PutExportSectionOneFunc(buf, exportName, n)
	return buf
}
