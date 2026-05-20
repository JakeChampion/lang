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

	// ParamValtypes are component-level cvaltype bytes for each
	// parameter (CValtype* constants).
	ParamValtypes []byte

	// CoreImportModule is the module name the core wasm module
	// uses in its `(import "X" "FuncName" ...)` declaration. The
	// component's instantiate-with-args section will pass the
	// packaged core instance under this name.
	CoreImportModule string
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
		buf = PutTypeSectionOneInstanceOneFuncNoResultExport(buf, imp.FuncName, imp.ParamNames, imp.ParamValtypes)
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
