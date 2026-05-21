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
//   1. Declares an instance type per WASI import describing its
//      interface.
//   2. Imports each interface under its WASI name (e.g.
//      "wasi:cli/exit@0.2.0").
//   3. Aliases each interface's function as a component-level func.
//   4. Lowers each component-level func to a core func.
//   5. Packages each lowered core func into a single-export core
//      instance.
//   6. Embeds the core module.
//   7. Instantiates the core module, passing each packaged core
//      instance under its CoreImportModule name.
//
// This is the Go-side counterpart of
// `build_wasi_multi_imported_component` in std/wasm/component.lang.
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
//   1. core-module:    embed the core module
//   2. core-instance:  instantiate (no args)
//   3. alias:          alias the named core export as a core-func
//   4. type:           declare the component-level functype
//   5. canon:          lift core-func 0 → component-func 0 (no opts)
//   6. export:         expose component-func 0 as exportName
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

	// Type 4: wasi:cli/stdout instance type (references type 3).
	// Import as instance 2.
	buf = PutTypeSectionRawBody(buf, WasiCliStdoutInstanceTypeBody(3))
	buf = PutImportSectionOneInstance(buf, "wasi:cli/stdout@0.2.0", 4)

	// Core modules: user (0), trampoline (1), fixup (2).
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreModuleSection(buf, TrampolineModuleFor4I32NoResult())
	buf = PutCoreModuleSection(buf, FixupModuleFor4I32NoResult())

	// Instantiate trampoline (no args) → core instance 0.
	buf = PutCoreInstanceSectionInstantiate(buf, 1)

	// Alias get-stdout from instance 2 → component func 0,
	// canon-lower it (no opts — `() -> handle` doesn't need
	// memory) → core func 0, wrap as core instance 1.
	buf = PutAliasSectionInstanceExportFunc(buf, 2, "get-stdout")
	buf = PutCanonSectionLowerNoOpts(buf, 0)
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "get-stdout", 0)

	// Alias "0" func from trampoline (core instance 0) → core
	// func 1. Wrap as core instance 2 under the method name.
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "[method]output-stream.blocking-write-and-flush", 1)

	// Instantiate user core module with stdout=instance 1,
	// streams=instance 2 (the trampoline wrapper) → core
	// instance 3.
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0,
		[]string{"wasi:cli/stdout@0.2.0", "wasi:io/streams@0.2.0"},
		[]uint32{1, 2})

	// Alias memory from the user core instance + the trampoline's
	// table. We don't alias cabi_realloc — our canon-lower uses
	// memory-only opts and the result-return area lives in the
	// bump heap (allocated via __lang_alloc), not via realloc.
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
	buf = PutAliasSectionCoreExportFunc(buf, 3, coreExportName)
	buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf, 5)
	buf = PutCanonSectionLiftNoOpts(buf, 3, 6)
	buf = PutInstanceSectionOnePackagedFunc(buf, "run", 2)
	buf = PutExportSectionOneInstance(buf, "wasi:cli/run@0.2.0", 3)
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
