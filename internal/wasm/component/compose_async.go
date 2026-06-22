package component

// compose_async.go assembles a runnable WASI Preview-3 component for a consumer
// core that AWAITS a single async import — the colorless async-import payoff
// (docs/WASI-PREVIEW3-ASYNC-PLAN.md). It is the composer half of the vertical
// whose wasmbin half emits the async-lower import shape (`dep$import` with the
// `(scalar params…, retptr) -> i32 status` signature + a colorless wrapper):
// here that raw import is lowered with `canon lower async` and wired to a
// bundled provider, and the consumer's own async core export is lifted with
// `canon lift async`, so both async-ABI directions run together.
//
// The provider is bundled as a NESTED component (so the result is a single
// self-contained component runnable under `wasmtime --invoke`, with no host
// needing to satisfy the async import). The async lower needs the consumer's
// linear memory, but the consumer is instantiated with the lowered func as an
// import — the same memory-option circularity the P2 `gMem` lowerings hit. It is
// broken the same way: a trampoline core module exports a funcref-table
// placeholder the consumer imports; after the consumer is instantiated its
// memory is aliased, the real `canon lower async` is emitted over that memory,
// and a fixup module patches the table slot to the real lowered func. So the
// real composer reuses the proven trampoline machinery rather than the spike's
// externalized shared memory.

// AsyncImportSpec describes one async import a consumer awaits: the
// `(iface, witName)` it imports, the `provider` component that satisfies it
// (exporting `providerExportName: async func(...) -> resultValtype`, bundled
// nested), and the `canon lower async` core signature `(lowerParams…) ->
// lowerResults` the import lowers to (`(scalar params…, retptr) -> i32 status`).
type AsyncImportSpec struct {
	Iface, WITName     string
	Provider           []byte
	ProviderExportName string
	LowerParams        []byte
	LowerResults       []byte
}

// BuildAsyncImportAwaitComponent is the single-import case of
// BuildAsyncImportsAwaitComponent, kept as a thin wrapper. See that function.
func BuildAsyncImportAwaitComponent(
	consumerCore []byte,
	importIface, importWITName string,
	provider []byte, providerExportName string,
	consumerAsyncExport, liftExportName string,
	lowerParams, lowerResults []byte,
	resultValtype byte,
) []byte {
	return BuildAsyncImportsAwaitComponent(consumerCore, []AsyncImportSpec{{
		Iface: importIface, WITName: importWITName,
		Provider: provider, ProviderExportName: providerExportName,
		LowerParams: lowerParams, LowerResults: lowerResults,
	}}, consumerAsyncExport, liftExportName, resultValtype)
}

// BuildAsyncImportsAwaitComponent wraps `consumerCore` (a Fern core that imports
// N async functions plus `("", "task-return")`, exports its linear memory, and
// exposes an async core function `consumerAsyncExport`) into a component that
// lifts `consumerAsyncExport` async under `liftExportName` and satisfies every
// async import from its bundled nested provider. Each import is lowered with
// `canon lower async` over the consumer's memory via its own gMem trampoline +
// fixup (breaking the lower→memory→instance circularity), so a handler that
// awaits several upstreams composes. The single-import path
// (BuildAsyncImportAwaitComponent) is N=1 and emits byte-identical output.
//
// Each import's interface must be distinct (one core-instance import arg per
// module name); scalar params + scalar result per the proven `dep(): i32`
// shape.
func BuildAsyncImportsAwaitComponent(
	consumerCore []byte,
	imports []AsyncImportSpec,
	consumerAsyncExport, liftExportName string,
	resultValtype byte,
) []byte {
	n := uint32(len(imports))
	buf := PutComponentHeader(nil)

	// Phase 1: nest each provider, instantiate it, alias its async export.
	// Provider i → component i, component instance i, component func i (depCFunc[i] = i).
	for i := range imports {
		buf = PutComponentSection(buf, imports[i].Provider)
		buf = PutInstanceSectionInstantiateComponent(buf, uint32(i))
		buf = PutAliasSectionInstanceExportFunc(buf, uint32(i), imports[i].ProviderExportName)
	}

	// Phase 2: a trampoline module + instance per import (core module i, core
	// instance i) — the funcref-table placeholder that breaks the
	// lower→memory→consumer circularity.
	for i := range imports {
		buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(imports[i].LowerParams, imports[i].LowerResults))
		buf = PutCoreInstanceSectionInstantiate(buf, uint32(i))
	}

	// Phase 3: task.return → core func 0.
	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	// Phase 4: placeholder dep-lower per import (trampoline "0") → core func 1+i.
	for i := range imports {
		buf = PutAliasSectionCoreExportFunc(buf, uint32(i), "0")
	}

	// Phase 5: consumer core module → core module n.
	consumerMod := n
	buf = PutCoreModuleSection(buf, consumerCore)

	// Phase 6: import-arg instances. Module "" provides task-return (core func
	// 0); each import's interface provides its wit-name wired to that import's
	// trampoline placeholder (core func 1+i). Then instantiate the consumer.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	}) // core instance n
	for i := range imports {
		buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
			{Name: imports[i].WITName, Sort: CoreSortFunc, Idx: 1 + uint32(i)},
		}) // core instance n+1+i
	}
	argNames := make([]string, 0, n+1)
	argInsts := make([]uint32, 0, n+1)
	argNames = append(argNames, "")
	argInsts = append(argInsts, n) // task-return arg instance
	for i := range imports {
		argNames = append(argNames, imports[i].Iface)
		argInsts = append(argInsts, n+1+uint32(i))
	}
	consumerInst := 2*n + 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, consumerMod, argNames, argInsts)

	// Phase 7: alias the consumer's memory → core memory 0.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, consumerInst, "memory")

	// Phase 8: the real `canon lower async` of each provider func (component
	// func i) over the consumer memory → core func n+1+i.
	for i := range imports {
		buf = PutCanonSectionLowerAsync(buf, uint32(i), 0)
	}

	// Phase 9: per import, a fixup module that patches its trampoline table
	// slot 0 to its real lowered func (core func n+1+i). Core table i is that
	// import's trampoline "$imports" table.
	for i := range imports {
		buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(imports[i].LowerParams, imports[i].LowerResults))
		buf = PutAliasSectionCoreExport(buf, CoreSortTable, uint32(i), "$imports") // core table i
		buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
			{Name: "$imports", Sort: CoreSortTable, Idx: uint32(i)},
			{Name: "0", Sort: CoreSortFunc, Idx: n + 1 + uint32(i)},
		})
		// The fixup module index is n (consumer) + 1 + i.
		buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, consumerMod+1+uint32(i),
			[]string{""}, []uint32{argFixupInst(n, i)})
	}

	// Phase 10: lift the consumer's async core export async under liftExportName.
	// Core funcs: 0 (task.return), 1..n (placeholders), n+1..2n (real lowers),
	// then runCoreF = 2n+1. Component funcs: 0..n-1 (provider aliases), then
	// the lift is component func n.
	runCoreF := 2*n + 1
	buf = PutAliasSectionCoreExportFunc(buf, consumerInst, consumerAsyncExport) // core func 2n+1
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype)                   // type 0
	buf = PutCanonSectionLiftAsync(buf, runCoreF, 0)                            // component func n
	buf = PutExportSectionOneFunc(buf, liftExportName, n)
	return buf
}

// argFixupInst returns the core-instance index of import i's fixup arg
// instance. Core instances in order: 0..n-1 trampolines, n task-return arg,
// n+1..2n per-import args, 2n+1 consumer, then for each import a (fixup-arg,
// fixup) pair starting at 2n+2 — so import i's fixup arg is at 2n+2 + 2*i.
func argFixupInst(n uint32, i int) uint32 { return 2*n + 2 + 2*uint32(i) }
