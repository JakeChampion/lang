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
	// NeedsRealloc selects the `canon lower async + realloc` form for a result
	// that carries linear-memory data (a `string` / `list<T>`): the canonical
	// ABI materialises the incoming bytes in the consumer's memory via its
	// cabi_realloc, which the composer aliases and threads into the lower. A
	// scalar result leaves this false (lower carries only `[async, memory]`).
	NeedsRealloc bool
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
// N async functions plus the canon glue under `""` — `task-return` and the
// waitable-set / subtask intrinsics `ws-new`/`w-join`/`ws-wait`/`subtask-drop`/
// `ws-drop` the async-import wrapper's pending-await loop calls — exports its
// linear memory, and exposes an async core function `consumerAsyncExport`) into
// a component that lifts `consumerAsyncExport` async under `liftExportName` and
// satisfies every async import from its bundled nested provider. Each import is
// lowered with `canon lower async` over the consumer's memory via its own gMem
// trampoline + fixup (breaking the lower→memory→instance circularity), so a
// handler that awaits several upstreams composes. The single-import path
// (BuildAsyncImportAwaitComponent) is N=1 and emits byte-identical output.
//
// The four memory-independent waitable canon funcs (ws-new/w-join/subtask-drop/
// ws-drop) are emitted directly as core funcs in the `""` instance alongside
// task-return. `ws-wait` carries a `memory` option referencing the consumer's
// exported memory (aliased only after the consumer is instantiated), so — like
// each dep-lower — it goes through its own gMem trampoline + fixup. A sync
// import never reaches the loop (the lower returns RETURNED), but the imports
// must still be provided because the consumer core declares them.
//
// Each import's interface must be distinct (one core-instance import arg per
// module name) and distinct from `""`; scalar params + scalar result per the
// proven `dep(): i32` shape (string/list results enable NeedsRealloc).
//
// Indices are tracked with running counters rather than closed-form formulas:
// the layout interleaves several index spaces (core func / core instance / core
// module / core table / core memory / component func) and the waitable glue made
// the formulas unmaintainable.
func BuildAsyncImportsAwaitComponent(
	consumerCore []byte,
	imports []AsyncImportSpec,
	consumerAsyncExport, liftExportName string,
	resultValtype byte,
) []byte {
	n := len(imports)
	buf := PutComponentHeader(nil)

	// Running index counters, one per (separate) index space.
	var cf, ci, cm, ct, compc, compi, compf uint32

	// Phase 1: nest each provider, instantiate it, alias its async export →
	// component func depCFunc[i].
	depCFunc := make([]uint32, n)
	for i := range imports {
		buf = PutComponentSection(buf, imports[i].Provider) // sub-component compc
		buf = PutInstanceSectionInstantiateComponent(buf, compc)
		compc++
		buf = PutAliasSectionInstanceExportFunc(buf, compi, imports[i].ProviderExportName)
		compi++
		depCFunc[i] = compf
		compf++
	}

	// Phase 2: a trampoline module + instance per import, plus one for ws-wait —
	// the funcref-table placeholders that break the lower/wait → memory → consumer
	// circularity (each real canon func references the consumer's memory).
	depTrampInst := make([]uint32, n)
	for i := range imports {
		buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(imports[i].LowerParams, imports[i].LowerResults))
		cm++
		buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
		depTrampInst[i] = ci
		ci++
	}
	wsWaitParams, wsWaitResults := []byte{0x7f, 0x7f}, []byte{0x7f} // (set i32, evtptr i32) -> i32 status
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(wsWaitParams, wsWaitResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	wsWaitTrampInst := ci
	ci++

	// Phase 3: the canon glue that does NOT need the consumer memory → direct
	// core funcs (these go in the "" instance).
	trCoreF := cf
	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	cf++
	wsNewCoreF := cf
	buf = PutCanonWaitableSetNew(buf)
	cf++
	wJoinCoreF := cf
	buf = PutCanonWaitableJoin(buf)
	cf++
	subtaskDropCoreF := cf
	buf = PutCanonSubtaskDrop(buf)
	cf++
	wsDropCoreF := cf
	buf = PutCanonWaitableSetDrop(buf)
	cf++

	// Phase 4: placeholder dep-lower per import + placeholder ws-wait, aliased
	// out of their trampoline instances ("0").
	depPlaceholderF := make([]uint32, n)
	for i := range imports {
		buf = PutAliasSectionCoreExportFunc(buf, depTrampInst[i], "0")
		depPlaceholderF[i] = cf
		cf++
	}
	buf = PutAliasSectionCoreExportFunc(buf, wsWaitTrampInst, "0")
	wsWaitPlaceholderF := cf
	cf++

	// Phase 5: consumer core module.
	buf = PutCoreModuleSection(buf, consumerCore)
	consumerMod := cm
	cm++

	// Phase 6: import-arg instances + consumer instantiation. The "" instance
	// provides task-return + the five waitable intrinsics (ws-wait via its
	// placeholder); each import's interface provides its wit-name wired to that
	// import's dep-lower placeholder.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: trCoreF},
		{Name: "ws-new", Sort: CoreSortFunc, Idx: wsNewCoreF},
		{Name: "w-join", Sort: CoreSortFunc, Idx: wJoinCoreF},
		{Name: "ws-wait", Sort: CoreSortFunc, Idx: wsWaitPlaceholderF},
		{Name: "subtask-drop", Sort: CoreSortFunc, Idx: subtaskDropCoreF},
		{Name: "ws-drop", Sort: CoreSortFunc, Idx: wsDropCoreF},
	})
	emptyInst := ci
	ci++
	importInst := make([]uint32, n)
	for i := range imports {
		buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
			{Name: imports[i].WITName, Sort: CoreSortFunc, Idx: depPlaceholderF[i]},
		})
		importInst[i] = ci
		ci++
	}
	argNames := make([]string, 0, n+1)
	argInsts := make([]uint32, 0, n+1)
	argNames = append(argNames, "")
	argInsts = append(argInsts, emptyInst)
	for i := range imports {
		argNames = append(argNames, imports[i].Iface)
		argInsts = append(argInsts, importInst[i])
	}
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, consumerMod, argNames, argInsts)
	consumerInst := ci
	ci++

	// Phase 7: alias the consumer's memory → core memory 0, and (if any import
	// returns a string/list) its exported cabi_realloc.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, consumerInst, "memory")
	mem := uint32(0) // the consumer's memory is the only one → core memory 0
	needRealloc := false
	for i := range imports {
		if imports[i].NeedsRealloc {
			needRealloc = true
		}
	}
	var reallocCoreF uint32
	if needRealloc {
		buf = PutAliasSectionCoreExport(buf, CoreSortFunc, consumerInst, "cabi_realloc")
		reallocCoreF = cf
		cf++
	}

	// Phase 8: the real lower of each provider func over the consumer memory, and
	// the real ws-wait over it.
	depRealF := make([]uint32, n)
	for i := range imports {
		if imports[i].NeedsRealloc {
			buf = PutCanonSectionLowerAsyncRealloc(buf, depCFunc[i], mem, reallocCoreF)
		} else {
			buf = PutCanonSectionLowerAsync(buf, depCFunc[i], mem)
		}
		depRealF[i] = cf
		cf++
	}
	buf = PutCanonWaitableSetWait(buf, mem)
	wsWaitRealF := cf
	cf++

	// Phase 9: per import (and ws-wait), a fixup module that patches the
	// trampoline table slot 0 to the real func.
	for i := range imports {
		buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(imports[i].LowerParams, imports[i].LowerResults))
		fixupMod := cm
		cm++
		buf = PutAliasSectionCoreExport(buf, CoreSortTable, depTrampInst[i], "$imports")
		tbl := ct
		ct++
		buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
			{Name: "$imports", Sort: CoreSortTable, Idx: tbl},
			{Name: "0", Sort: CoreSortFunc, Idx: depRealF[i]},
		})
		fixupArgInst := ci
		ci++
		buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, fixupMod, []string{""}, []uint32{fixupArgInst})
		ci++
	}
	// ws-wait fixup.
	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(wsWaitParams, wsWaitResults))
	wsWaitFixupMod := cm
	cm++
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, wsWaitTrampInst, "$imports")
	wsWaitTbl := ct
	ct++
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: wsWaitTbl},
		{Name: "0", Sort: CoreSortFunc, Idx: wsWaitRealF},
	})
	wsWaitFixupArgInst := ci
	ci++
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, wsWaitFixupMod, []string{""}, []uint32{wsWaitFixupArgInst})
	ci++

	// Phase 10: lift the consumer's async core export async under liftExportName.
	buf = PutAliasSectionCoreExportFunc(buf, consumerInst, consumerAsyncExport)
	runCoreF := cf
	cf++
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype) // component type 0
	buf = PutCanonSectionLiftAsync(buf, runCoreF, 0)
	liftCompF := compf
	compf++
	buf = PutExportSectionOneFunc(buf, liftExportName, liftCompF)
	return buf
}

// BuildAsyncLiftedExportComponentString wraps a reactor `providerCore` whose
// `coreExportName` export delivers a STRING result through an imported
// `task.return` (core sig `(ptr, len) -> ()`, the bytes living in the core's
// own linear memory exported as `coreMemExportName`) into a component exporting
// `exportName: async func() -> string`. Unlike the scalar
// BuildAsyncLiftedExportComponent, the string `task.return` carries a `memory`
// option that references the provider's memory — but the provider imports
// task.return, so the memory→instance→import dependency is circular. It is
// broken with the same gMem trampoline used for the import lower: the provider
// imports a funcref-table placeholder for task.return, its memory is aliased
// after instantiation, the real `canon task.return (string) (memory)` is emitted
// over that memory, and a fixup patches the table slot. The async lift carries
// the memory option too (a string result reads from the core memory).
func BuildAsyncLiftedExportComponentString(providerCore []byte, coreMemExportName, coreExportName, exportName string) []byte {
	// task.return for a string lowers to a core func `(ptr i32, len i32) -> ()`.
	trParams := []byte{0x7f, 0x7f}
	buf := PutComponentHeader(nil)

	// Trampoline module 0 → core instance 0 (placeholder task.return + table).
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(trParams, nil))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)  // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0") // core func 0 (placeholder)

	// Provider core module 1, instantiated with the placeholder bound to its
	// ("", "task-return") import.
	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (provider)

	// Alias the provider memory → core memory 0, then emit the real string
	// task.return over it (core func 1).
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, coreMemExportName) // core memory 0
	buf = PutCanonTaskReturnStringWithMemory(buf, 0)                           // core func 1

	// Fixup module 2: patch the trampoline table slot 0 to the real task.return.
	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(trParams, nil))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports") // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 1},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	// Alias the provider's export → core func 2, lift it async (with memory,
	// since the result is a string) as exportName.
	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName) // core func 2
	buf = PutTypeSectionOneFunc(buf, nil, nil, cValtypeString)  // type 0: () -> string
	buf = PutCanonSectionLiftAsyncWithMemory(buf, 2, 0, 0)      // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentStringParamStringResult lifts a core that takes
// a STRING parameter AND returns a STRING result into `exportName: async
// func(s: string) -> string` — the composite-param-and-result edge shape (e.g.
// an HTTP `fetch(url) -> body`). It combines the two composite directions:
//   - the incoming `string` param is materialised in the export's memory via its
//     bump cabi_realloc before the core runs (the `[memory, realloc]` lift), and
//   - the `string` result is delivered through an imported string `task.return`
//     (core sig `(ptr, len) -> ()`) whose `memory` option references the
//     provider's own memory — circular because the provider imports task.return,
//     so it is broken with the same gMem trampoline as
//     BuildAsyncLiftedExportComponentString (placeholder task.return → alias
//     memory → real `task.return (string) (memory)` → fixup the table slot).
//
// The core must export its memory (memExportName) and a real bump cabi_realloc
// (reallocExportName), and import `("", "task-return")` (the string-flavored
// `(ptr, len) -> ()`); `coreExportName` is its `(ptr, len) -> ()` worker.
func BuildAsyncLiftedExportComponentStringParamStringResult(providerCore []byte, memExportName, reallocExportName, coreExportName, exportName string) []byte {
	trParams := []byte{0x7f, 0x7f} // string task.return: (ptr, len) -> ()
	buf := PutComponentHeader(nil)

	// Trampoline module 0 → core instance 0 (placeholder task.return + table).
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(trParams, nil))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)  // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0") // core func 0 (placeholder task.return)

	// Provider core module 1, instantiated with the placeholder bound to its
	// ("", "task-return") import.
	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (provider)

	// Alias the provider memory → core memory 0, then the real string task.return
	// over it (core func 1).
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, memExportName) // core memory 0
	buf = PutCanonTaskReturnStringWithMemory(buf, 0)                       // core func 1

	// Fixup module 2: patch the trampoline table slot 0 to the real task.return.
	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(trParams, nil))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports") // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 1},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	// Alias the provider's cabi_realloc (the lift's realloc option for the incoming
	// param) and its worker export, then lift async with [memory, realloc] as
	// `(string) -> string`.
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 2, reallocExportName) // core func 2 (cabi_realloc)
	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName)              // core func 3 (worker)
	buf = PutTypeSectionOneFunc(buf, []string{"s"}, []byte{cValtypeString}, cValtypeString)
	buf = PutCanonSectionLiftAsyncWithMemoryRealloc(buf, 3, 0, 0, 2) // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentList is the `list<elem>` counterpart of
// BuildAsyncLiftedExportComponentString: it lifts a core whose `coreExportName`
// export delivers a `list<elem>` (its `(ptr, len)` bytes in the core memory
// `coreMemExportName`) through `task.return` into `exportName: async func() ->
// list<elem>`. At the canonical ABI a `list<elem>` is `(ptr, len)` just like a
// string, so the core shape, the task.return core sig `(ptr, len) -> ()`, and
// the gMem trampoline that breaks the task.return memory circularity are
// identical to the string provider — the only difference is that the result is
// a defined `list<elem>` component type (referenced by index) instead of the
// inline `string` primitive. `elemValtype` is the element's component valtype
// (e.g. CValtypeU8 / CValtypeU32).
func BuildAsyncLiftedExportComponentList(providerCore []byte, coreMemExportName, coreExportName, exportName string, elemValtype byte) []byte {
	trParams := []byte{0x7f, 0x7f} // task.return for a list: (ptr, len) -> ()
	buf := PutComponentHeader(nil)

	// Component type 0: the `list<elem>` defined type (referenced by task.return
	// and the lift functype below).
	buf = PutTypeSectionOneDefined(buf, InnerTypeList(elemValtype)) // component type 0

	// Trampoline module 0 → core instance 0 (placeholder task.return + table).
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(trParams, nil))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)  // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0") // core func 0 (placeholder)

	// Provider core module 1, instantiated with the placeholder task.return.
	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (provider)

	// Alias the provider memory → core memory 0; real `task.return (list) (memory)`.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, coreMemExportName) // core memory 0
	buf = PutCanonTaskReturnTypeIdxWithMemory(buf, 0, 0)                       // core func 1 (result = component type 0)

	// Fixup module 2: patch the trampoline table slot 0 to the real task.return.
	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(trParams, nil))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports") // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 1},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	// Alias the provider export → core func 2; lift it async (with memory) as a
	// `() -> list<elem>` (component type 1, whose result references type 0).
	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName) // core func 2
	buf = PutTypeSectionOneFuncResultIdx(buf, nil, nil, 0)      // component type 1: () -> (type 0)
	buf = PutCanonSectionLiftAsyncWithMemory(buf, 2, 1, 0)      // component func 0 (functype 1)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentStringParam lifts a core `send` that takes a
// STRING parameter and returns a scalar (`coreExportName`, core sig
// `(ptr, len) -> ()` delivering its scalar result through the imported scalar
// `task.return`) into `exportName: async func(s: string) -> resultValtype`. A
// string parameter is materialised in the export's memory by the canonical ABI
// via the export's exported cabi_realloc before the core func runs, so the lift
// carries [async, memory, realloc]. Unlike the string/list RESULT providers
// there is no task.return memory-circularity: the result is a scalar, so
// task.return needs no memory option and the provider imports it directly (the
// lift's memory + realloc reference the provider instance, aliased after it is
// instantiated). The core must export its memory (memExportName) and a real
// bump `cabi_realloc` (reallocExportName) — a constant-returning realloc fails
// at runtime because the async ABI calls it more than once. Verified end to end
// (`send("hello") -> 5`) — docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func BuildAsyncLiftedExportComponentStringParam(providerCore []byte, memExportName, reallocExportName, coreExportName, exportName string, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	// Scalar task.return → core func 0; provider instantiated against it.
	buf = PutCanonTaskReturnSingle(buf, resultValtype)                                         // core func 0
	buf = PutCoreModuleSection(buf, providerCore)                                              // core module 0
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1 (provider)

	// Alias the provider's memory + cabi_realloc (the lift's options), then its
	// send export, and lift it async with [memory, realloc] as `(string) -> result`.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 1, memExportName)                 // core memory 0
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 1, reallocExportName)               // core func 1 (cabi_realloc)
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)                            // core func 2 (send)
	buf = PutTypeSectionOneFunc(buf, []string{"s"}, []byte{cValtypeString}, resultValtype) // component type 0
	buf = PutCanonSectionLiftAsyncWithMemoryRealloc(buf, 2, 0, 0, 1)                       // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentListParam is the numeric-`list<elem>` parameter
// counterpart of BuildAsyncLiftedExportComponentStringParam: it lifts a core
// `recv` that takes a `list<elem>` argument and returns a scalar into
// `exportName: async func(xs: list<elem>) -> resultValtype`. At the canonical
// ABI a `list<elem>` parameter flattens to `(ptr, len)` exactly like a string,
// so the core sig is the plain `(ptr, len) -> ()` (result via scalar
// task.return) and the lift carries the same `[async, memory, realloc]` options
// — the incoming elements are materialised in the export's memory via its bump
// cabi_realloc before the core runs. The only difference from the string-param
// builder is the parameter's component type: a *defined* `list<elem>` type
// (component type 0, referenced by index in the lift functype) instead of the
// inline `string` primitive. The core must export its memory (memExportName) and
// a real bump cabi_realloc (reallocExportName).
func BuildAsyncLiftedExportComponentListParam(providerCore []byte, memExportName, reallocExportName, coreExportName, exportName string, elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	// Component type 0: the `list<elem>` defined type (the lift functype's param).
	buf = PutTypeSectionOneDefined(buf, InnerTypeList(elemValtype)) // component type 0

	// Scalar task.return → core func 0; provider instantiated against it.
	buf = PutCanonTaskReturnSingle(buf, resultValtype)                                         // core func 0
	buf = PutCoreModuleSection(buf, providerCore)                                              // core module 0
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1 (provider)

	// Alias the provider's memory + cabi_realloc (the lift's options), then its
	// recv export, and lift it async with [memory, realloc] as `(list<elem>) -> result`.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 1, memExportName)   // core memory 0
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 1, reallocExportName) // core func 1 (cabi_realloc)
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)              // core func 2 (recv)
	// component type 1: func(xs: type 0) -> resultValtype.
	buf = PutTypeSectionOneFuncGeneral(buf, []string{"xs"}, [][]byte{leb128SlebBytes(0)}, []byte{resultValtype})
	buf = PutCanonSectionLiftAsyncWithMemoryRealloc(buf, 2, 1, 0, 1) // component func 0 (functype 1)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildAsyncLiftedExportComponentMemParams is the general multi-parameter
// counterpart of BuildAsyncLiftedExportComponentStringParam: it lifts a core
// `recv` taking an arbitrary mix of params and returning a scalar into
// `exportName: async func(<params…>) -> resultValtype` with a `[async, memory,
// realloc]` lift. Each parameter's component valtype is supplied pre-encoded in
// `paramVals` (a primitive byte such as `CValtypeString` / `CValtypeU32`, or the
// sleb-encoded index of a defined type emitted earlier). The core's signature is
// the canonical flattening of those params `-> ()` (result via scalar
// task.return); any incoming `string`/`list` arg is materialised in the export's
// memory via its bump cabi_realloc before the core runs. The core must export its
// memory (memExportName) and a real bump cabi_realloc (reallocExportName). Used
// for the multi-arg edge-handler shape (e.g. `fetch(url: string, timeout: u32)`).
func BuildAsyncLiftedExportComponentMemParams(providerCore []byte, memExportName, reallocExportName, coreExportName, exportName string, paramNames []string, paramVals [][]byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	// Scalar task.return → core func 0; provider instantiated against it.
	buf = PutCanonTaskReturnSingle(buf, resultValtype)                                         // core func 0
	buf = PutCoreModuleSection(buf, providerCore)                                              // core module 0
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1 (provider)

	// Alias the provider's memory + cabi_realloc (the lift's options), then its
	// recv export, and lift it async with [memory, realloc] as `(params…) -> result`.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 1, memExportName)   // core memory 0
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 1, reallocExportName) // core func 1 (cabi_realloc)
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)              // core func 2 (recv)
	buf = PutTypeSectionOneFuncGeneral(buf, paramNames, paramVals, []byte{resultValtype})
	buf = PutCanonSectionLiftAsyncWithMemoryRealloc(buf, 2, 0, 0, 1) // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// buildPendingProviderComponent is the spike's fixed-name pending provider
// (`dep: async func() -> u32`), kept as a thin wrapper over the general
// BuildPendingDeferringProviderComponent.
func buildPendingProviderComponent(providerCore []byte, resultValtype byte) []byte {
	return BuildPendingDeferringProviderComponent(providerCore, "dep", "dep", resultValtype)
}

// BuildPendingDeferringProviderComponent builds a nested provider sub-component
// `exportName: async func() -> resultValtype` that GENUINELY DEFERS: its core
// `coreExportName` first calls `thread.yield` (so the awaiting consumer's
// `canon lower async` of it returns a STARTED/pending status, forcing the
// caller's pending-await loop to run) and then `task.return`s its value.
// providerCore imports ("", "tr") task-return `(value) -> ()` and ("", "y")
// thread.yield `() -> (i32)`, and exports `coreExportName` `() -> ()`. This is
// the deferring counterpart of BuildAsyncLiftedExportComponent (whose provider
// completes synchronously / RETURNED); pairing it with a real wasmbin-generated
// consumer drives the async-import await loop down its pending path end to end.
func BuildPendingDeferringProviderComponent(providerCore []byte, coreExportName, exportName string, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCanonTaskReturnSingle(buf, resultValtype) // core func 0
	buf = PutCanonThreadYield(buf)                     // core func 1
	buf = PutCoreModuleSection(buf, providerCore)      // core module 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "y", Sort: CoreSortFunc, Idx: 1},
	}) // core instance 0
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)                                // core func 2
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype)                                  // type 0
	buf = PutCanonSectionLiftAsync(buf, 2, 0)                                                  // component func 0
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

// BuildPendingAwaitComponent assembles a runnable WASI Preview-3 component that
// awaits a *genuinely pending* async import — the deferring-provider /
// waitable-set await loop (docs/WASI-PREVIEW3-ASYNC-PLAN.md). The nested
// provider's `dep` `thread.yield`s before returning, so the consumer's
// `canon lower async` of it returns a STARTED status; the consumer core then
// drives the await state machine (`waitable-set.new` → `waitable.join` the
// subtask → `waitable-set.wait` → `subtask.drop` → read the return area) and
// `task.return`s the value, lifted async as `run`. The consumer's memory is
// externalized into a shared core module (sidestepping the lower/wait
// memory-option circularity for this spike; the production path reuses the gMem
// trampoline). consumerCore must import ("","tr"/"dl"/"wsn"/"wj"/"wsw"/"sd"/"wsd")
// and ("mem","m"), and export "run". Proven to return its value under
// `wasmtime -W component-model-async,component-model-async-stackful`.
func BuildPendingAwaitComponent(providerCore, consumerCore, memCore []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	// Nested provider → component 0, instance 0, alias dep → component func 0.
	buf = PutComponentSection(buf, buildPendingProviderComponent(providerCore, resultValtype))
	buf = PutInstanceSectionInstantiateComponent(buf, 0)
	buf = PutAliasSectionInstanceExportFunc(buf, 0, "dep") // component func 0 (dep)

	// Externalized consumer memory → core memory 0.
	buf = PutCoreModuleSection(buf, memCore)                     // core module 0
	buf = PutCoreInstanceSectionInstantiate(buf, 0)              // core instance 0
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 0, "m") // core memory 0

	// Consumer canon glue (core funcs 0..6).
	buf = PutCanonTaskReturnSingle(buf, resultValtype) // core func 0 (tr)
	buf = PutCanonSectionLowerAsync(buf, 0, 0)         // core func 1 (dl: lower dep async over memory 0)
	buf = PutCanonWaitableSetNew(buf)                  // core func 2 (wsn)
	buf = PutCanonWaitableJoin(buf)                    // core func 3 (wj)
	buf = PutCanonWaitableSetWait(buf, 0)              // core func 4 (wsw over memory 0)
	buf = PutCanonSubtaskDrop(buf)                     // core func 5 (sd)
	buf = PutCanonWaitableSetDrop(buf)                 // core func 6 (wsd)

	// Consumer core module 1, instantiated with the canon glue (module "") and
	// the shared memory (module "mem").
	buf = PutCoreModuleSection(buf, consumerCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "dl", Sort: CoreSortFunc, Idx: 1},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 2},
		{Name: "wj", Sort: CoreSortFunc, Idx: 3},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 4},
		{Name: "sd", Sort: CoreSortFunc, Idx: 5},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 6},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	// Lift the consumer's run async under "run".
	buf = PutAliasSectionCoreExportFunc(buf, 2, "run")        // core func 7
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype) // type 0
	buf = PutCanonSectionLiftAsync(buf, 7, 0)                 // component func 1
	buf = PutExportSectionOneFunc(buf, "run", 1)
	return buf
}

// BuildFutureRoundtripComponent assembles a runnable WASI Preview-3 component
// that exercises the `future<T>` async data channel end to end through the Go
// composer — the future counterpart of BuildPendingAwaitComponent. The consumer
// core creates a future (`future.new`, whose i64 packs readable=low32 /
// writable=high32), stores a value, `future.write`s it through the writable end,
// `future.read`s it back through the readable end (the write precedes the read in
// the same task, so the read completes synchronously / RETURNED — no await loop),
// and `task.return`s the value, lifted async as `run: async func() -> T`. The
// consumer's memory is externalised into a shared core module so the future
// read/write `memory` options reference memory 0 without the lower→memory→instance
// circularity (the production path reuses the gMem trampoline). consumerCore must
// import ("","tr"/"fnew"/"fwrite"/"fread") and ("mem","m"), and export "run".
// Proven to return its value under `wasmtime -W
// component-model-async,component-model-async-stackful`.
func BuildFutureRoundtripComponent(consumerCore, memCore []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	// Component type 0: the `future<resultValtype>` defined type (referenced by
	// future.new / future.write / future.read).
	buf = PutTypeSectionOneDefined(buf, InnerTypeFuture(resultValtype)) // component type 0

	// Externalised consumer memory → core memory 0.
	buf = PutCoreModuleSection(buf, memCore)                     // core module 0
	buf = PutCoreInstanceSectionInstantiate(buf, 0)              // core instance 0
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 0, "m") // core memory 0

	// Consumer canon glue (core funcs 0..3).
	buf = PutCanonTaskReturnSingle(buf, resultValtype) // core func 0 (tr)
	buf = PutCanonFutureNew(buf, 0)                    // core func 1 (fnew; future type 0)
	buf = PutCanonFutureWrite(buf, 0, 0)               // core func 2 (fwrite over memory 0)
	buf = PutCanonFutureRead(buf, 0, 0)                // core func 3 (fread over memory 0)

	// Consumer core module 1, instantiated with the canon glue (module "") and the
	// shared memory (module "mem").
	buf = PutCoreModuleSection(buf, consumerCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "fnew", Sort: CoreSortFunc, Idx: 1},
		{Name: "fwrite", Sort: CoreSortFunc, Idx: 2},
		{Name: "fread", Sort: CoreSortFunc, Idx: 3},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	// Lift the consumer's run async under "run" (scalar result T).
	buf = PutAliasSectionCoreExportFunc(buf, 2, "run")        // core func 4
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype) // component type 1
	buf = PutCanonSectionLiftAsync(buf, 4, 1)                 // component func 0
	buf = PutExportSectionOneFunc(buf, "run", 0)
	return buf
}

// BuildStreamRoundtripComponent assembles a runnable WASI Preview-3 component
// that exercises the `stream<elem>` async data channel end to end through the Go
// composer — the stream counterpart of BuildFutureRoundtripComponent. The
// consumer core creates a stream (`stream.new`, whose i64 packs readable=low32 /
// writable=high32), posts a `stream.read` for N elements, then `stream.write`s N
// elements through the writable end (read-before-write so the stackful async
// transfer resolves synchronously), and `task.return`s a scalar derived from the
// elements it read, lifted async as `run: async func() -> resultValtype`. The
// stream read/write `memory` options are over the externalised shared memory (the
// production path reuses the gMem trampoline). consumerCore must import
// ("","tr"/"snew"/"swrite"/"sread") and ("mem","m"), and export "run". Proven to
// return its value under `wasmtime -W
// component-model-async,component-model-async-stackful`.
func BuildStreamRoundtripComponent(consumerCore, memCore []byte, elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	// Component type 0: the `stream<elem>` defined type (referenced by
	// stream.new / stream.write / stream.read).
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype)) // component type 0

	// Externalised consumer memory → core memory 0.
	buf = PutCoreModuleSection(buf, memCore)                     // core module 0
	buf = PutCoreInstanceSectionInstantiate(buf, 0)              // core instance 0
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 0, "m") // core memory 0

	// Consumer canon glue (core funcs 0..3).
	buf = PutCanonTaskReturnSingle(buf, resultValtype) // core func 0 (tr)
	buf = PutCanonStreamNew(buf, 0)                    // core func 1 (snew; stream type 0)
	buf = PutCanonStreamWrite(buf, 0, 0)               // core func 2 (swrite over memory 0)
	buf = PutCanonStreamRead(buf, 0, 0)                // core func 3 (sread over memory 0)

	// Consumer core module 1, instantiated with the canon glue (module "") and the
	// shared memory (module "mem").
	buf = PutCoreModuleSection(buf, consumerCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "snew", Sort: CoreSortFunc, Idx: 1},
		{Name: "swrite", Sort: CoreSortFunc, Idx: 2},
		{Name: "sread", Sort: CoreSortFunc, Idx: 3},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	// Lift the consumer's run async under "run" (scalar result).
	buf = PutAliasSectionCoreExportFunc(buf, 2, "run")        // core func 4
	buf = PutTypeSectionOneFunc(buf, nil, nil, resultValtype) // component type 1
	buf = PutCanonSectionLiftAsync(buf, 4, 1)                 // component func 0
	buf = PutExportSectionOneFunc(buf, "run", 0)
	return buf
}
