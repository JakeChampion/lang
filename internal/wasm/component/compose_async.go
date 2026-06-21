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

// BuildAsyncImportAwaitComponent wraps `consumerCore` (a Fern core that imports
// one async function `(importIface, importWITName)` lowered as
// `(lowerParams…) -> lowerResults` and `("", "task-return")`, and exports its
// linear memory plus an async core function `consumerAsyncExport`) into a
// component that lifts `consumerAsyncExport` async under `liftExportName` and
// satisfies the async import from `provider` (a component exporting
// `providerExportName: async func(...) -> resultValtype`, e.g. one built by
// BuildAsyncLiftedExportComponent). This slice covers a single scalar async
// import + a scalar async export — the proven `dep(): i32` / `run` shape.
func BuildAsyncImportAwaitComponent(
	consumerCore []byte,
	importIface, importWITName string,
	provider []byte, providerExportName string,
	consumerAsyncExport, liftExportName string,
	lowerParams, lowerResults []byte,
	resultValtype byte,
) []byte {
	buf := PutComponentHeader(nil)

	// Provider nested → component 0, component instance 0, alias its async
	// export → component func 0 (the func we lower async below).
	buf = PutComponentSection(buf, provider)
	buf = PutInstanceSectionInstantiateComponent(buf, 0)
	buf = PutAliasSectionInstanceExportFunc(buf, 0, providerExportName) // component func 0

	// Trampoline module 0 → core instance 0: exports a placeholder func "0"
	// (matching the lowered signature) that call_indirects through its
	// "$imports" table, breaking the lower→memory→consumer circularity.
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(lowerParams, lowerResults))
	buf = PutCoreInstanceSectionInstantiate(buf, 0) // core instance 0

	// task.return → core func 0; placeholder dep-lower (trampoline "0") → core func 1.
	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")

	// Consumer core module 1, instantiated with two import-arg instances:
	// module "" provides task-return (core func 0); module importIface provides
	// importWITName wired to the trampoline placeholder (core func 1).
	buf = PutCoreModuleSection(buf, consumerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	}) // core instance 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: importWITName, Sort: CoreSortFunc, Idx: 1},
	}) // core instance 2
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1,
		[]string{"", importIface}, []uint32{1, 2}) // core instance 3 (consumer)

	// Alias the consumer's memory → core memory 0, then emit the real
	// `canon lower async` of the provider's func (component func 0) over it.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 3, "memory") // core memory 0
	buf = PutCanonSectionLowerAsync(buf, 0, 0)                        // core func 2 (real dep-lower)

	// Fixup module 2: patch the trampoline table slot 0 to the real lowered func.
	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(lowerParams, lowerResults))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports") // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 2},
	}) // core instance 4
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2,
		[]string{""}, []uint32{4}) // core instance 5 (fixup)

	// Lift the consumer's async core export async under liftExportName.
	buf = PutAliasSectionCoreExportFunc(buf, 3, consumerAsyncExport) // core func 3
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype)        // type 0
	buf = PutCanonSectionLiftAsync(buf, 3, 0)                        // component func 1
	buf = PutExportSectionOneFunc(buf, liftExportName, 1)
	return buf
}
