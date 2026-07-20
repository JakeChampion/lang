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
// BuildAsyncLiftedExportComponent wraps a core module whose named
// export delivers its single result through an imported `task-return`
// (then returns void) into a WASI Preview-3 component-model-async
// component: the export becomes `<exportName>: async func() ->
// <resultValtype>`. The core module must import `("", "task-return")`
// and call it before returning — the shape wasmbin emits under
// BuildOptions.AsyncExportName. Proven end-to-end (returns its result
// under `wasmtime -W component-model-async,component-model-async-stackful`);
// see docs/WASI-PREVIEW3-ASYNC-PLAN.md.
//
// Recipe: canon task.return → core-func 0; embed module; a core
// instance exporting "task-return"=core-func 0; instantiate the module
// with it bound to the empty import module; alias the named core export
// → core-func 1; func()->result at type 0; lift core-func 1 async →
// component-func 0; export it.
func BuildAsyncLiftedExportComponent(coreBytes []byte, coreExportName, exportName string, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCanonTaskReturnSingle(buf, resultValtype)                                         // core func 0
	buf = PutCoreModuleSection(buf, coreBytes)                                                 // core module 0
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)                                // core func 1
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)                             // type 0: async () -> result
	buf = PutCanonSectionLiftAsync(buf, 1, 0)                                                  // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentParams is BuildAsyncLiftedExportComponent
// generalised over a parameter list: the export becomes `<exportName>: async
// func(<params>) -> <resultValtype>`. The core export takes the params directly
// (`(params…) -> ()`) and delivers its result through the imported
// `task-return`. paramValtypes are component valtype bytes (e.g. CValtypeU32),
// parallel to paramNames. Used to build a param-taking async provider for the
// colorless async-import await path (docs/WASI-PREVIEW3-ASYNC-PLAN.md).
func BuildAsyncLiftedExportComponentParams(coreBytes []byte, coreExportName, exportName string, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCanonTaskReturnSingle(buf, resultValtype)                                         // core func 0
	buf = PutCoreModuleSection(buf, coreBytes)                                                 // core module 0
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)                                // core func 1
	buf = PutTypeSectionOneFuncAsync(buf, paramNames, paramValtypes, resultValtype)            // type 0: async (params) -> result
	buf = PutCanonSectionLiftAsync(buf, 1, 0)                                                  // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentTupleParam lifts a core `recv` whose canonical
// flattening takes the elements of a `tuple<elems…>` (the core sig is
// `(elem0, elem1, …) -> ()`, delivering a scalar result via task.return) into
// `exportName: async func(p: tuple<elems…>) -> resultValtype`. A tuple of scalars
// flattens straight to its element core types — no memory/realloc option — so this
// is the plain async lift of BuildAsyncLiftedExportComponentParams with the params
// grouped into a single defined `tuple` component type (referenced by index in
// the lift functype). Used to satisfy a real Fern async `@import` whose parameter
// is a tuple/record (the wasmbin side marshals the Fern tuple value to the same
// flattened element args via the shared emitExternParamMarshal head).
func BuildAsyncLiftedExportComponentTupleParam(coreBytes []byte, coreExportName, exportName string, elemValtypes []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeTuple(elemValtypes))                          // component type 0: tuple<elems…>
	buf = PutCanonTaskReturnSingle(buf, resultValtype)                                         // core func 0
	buf = PutCoreModuleSection(buf, coreBytes)                                                 // core module 0
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)                                // core func 1
	// component type 1: async func(p: tuple<elems…>(type 0)) -> resultValtype.
	buf = PutTypeSectionOneFuncGeneralAsync(buf, []string{"p"}, [][]byte{leb128SlebBytes(0)}, []byte{resultValtype})
	buf = PutCanonSectionLiftAsync(buf, 1, 1) // component func 0 (functype 1)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

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
