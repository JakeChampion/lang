package component

import "fmt"

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
	// ImportResultValtype is the component result valtype of THIS import's
	// async function (the provider's export result). The consumer component
	// declares each import's functype explicitly (`() -> ImportResultValtype`),
	// which can differ from the consumer's own export result — e.g. an import
	// `big(): u64` awaited inside `run(): i32`. Zero falls back to the
	// consumer's resultValtype (the common scalar case where they match).
	ImportResultValtype byte
	// ImportParamNames / ImportParamVals declare THIS import's component-level
	// parameters (the provider's export params), each valtype pre-encoded (a
	// primitive's single byte, e.g. CValtypeU32 / cValtypeString, or the
	// sleb-encoded index of an ImportDefinedType). Empty = a no-param import.
	// ImportResultVal, when non-nil, overrides ImportResultValtype with a
	// pre-encoded result valtype (needed when the result is a defined type such
	// as `list<T>`). ImportDefinedTypes are defined-type bodies (e.g.
	// InnerTypeList(elem)) emitted immediately before this import's functype;
	// they take the component type indices 0..k-1 (single-import composition),
	// which the sleb valtype refs above point at.
	ImportParamNames   []string
	ImportParamVals    [][]byte
	ImportResultVal    []byte
	ImportDefinedTypes [][]byte
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
	// The consumer machinery (below) runs inside a nested component that
	// IMPORTS each async function it awaits. The outer component
	// (buildAsyncImportsAwaitOuter) instantiates a sibling provider per
	// import and links it in. This sibling structure is required by
	// wasmtime v46: its component-model reentrancy check traps
	// ("cannot enter component instance") when a consumer core module lowers
	// a provider bundled in the SAME component instance and calls it, but a
	// clean cross-nested-component call is allowed. See
	// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
	inner := buildAsyncConsumerComponent(consumerCore, imports, consumerAsyncExport, liftExportName, resultValtype)
	return buildAsyncImportsAwaitOuter(inner, imports, liftExportName)
}

// buildAsyncConsumerComponent builds the consumer half as a standalone
// component that imports each awaited async function (named "dep<i>", with the
// async functype `() -> resultValtype`) and lifts `consumerAsyncExport` async
// under `liftExportName`. It is the former body of BuildAsyncImportsAwaitComponent
// with phase 1 (bundled providers) replaced by component-level func imports:
// the awaited funcs now come from the enclosing component instead of from
// providers nested INSIDE this one, so the consumer→provider call crosses a
// component-instance boundary (required on wasmtime v46 — see the caller).
func buildAsyncConsumerComponent(
	consumerCore []byte,
	imports []AsyncImportSpec,
	consumerAsyncExport, liftExportName string,
	resultValtype byte,
) []byte {
	n := len(imports)
	buf := PutComponentHeader(nil)

	// Running index counters, one per (separate) index space.
	var cf, ci, cm, ct, compf, compType uint32

	// Phase 1: declare each awaited async function's component functype
	// (async form 0x43, `() -> resultValtype`) and import it as component
	// func depCFunc[i]. The import name "dep<i>" is matched by the outer
	// component's instantiation arg.
	depCFunc := make([]uint32, n)
	importNames := make([]string, n)
	typeidxs := make([]uint32, n)
	for i := range imports {
		spec := imports[i]
		// Any defined types this import's signature references (list<T> /
		// tuple<…>) come first, taking component type indices before the
		// functype.
		for _, dt := range spec.ImportDefinedTypes {
			buf = PutTypeSectionOneDefined(buf, dt)
			compType++
		}
		// The import functype. Pre-encoded param/result valtypes (general form)
		// take precedence; otherwise the scalar `() -> irv` shape.
		if len(spec.ImportParamVals) > 0 || spec.ImportResultVal != nil {
			resultVal := spec.ImportResultVal
			if resultVal == nil {
				irv := spec.ImportResultValtype
				if irv == 0 {
					irv = resultValtype
				}
				resultVal = []byte{irv}
			}
			buf = PutTypeSectionOneFuncGeneralAsync(buf, spec.ImportParamNames, spec.ImportParamVals, resultVal)
		} else {
			irv := spec.ImportResultValtype
			if irv == 0 {
				irv = resultValtype
			}
			buf = PutTypeSectionOneFuncAsync(buf, nil, nil, irv)
		}
		typeidxs[i] = compType
		compType++
		importNames[i] = fmt.Sprintf("dep%d", i)
	}
	buf = PutComponentImportSectionFuncs(buf, importNames, typeidxs)
	for i := range imports {
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
	// The lift functype lands after the n import functypes → component type
	// index compType.
	buf = PutAliasSectionCoreExportFunc(buf, consumerInst, consumerAsyncExport)
	runCoreF := cf
	cf++
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype) // component type compType
	buf = PutCanonSectionLiftAsync(buf, runCoreF, compType)
	compType++
	liftCompF := compf
	compf++
	buf = PutExportSectionOneFunc(buf, liftExportName, liftCompF)
	return buf
}

// buildAsyncImportsAwaitOuter wraps the consumer component `inner` (which
// imports "dep0".."depN-1") together with one sibling provider per import:
// each provider is nested + instantiated, its async export aliased to a
// component func, and all are fed into `inner` as its "dep<i>" args. `inner`'s
// `liftExportName` export is re-exported. Producing the sibling structure the
// v46 component-model reentrancy rules require (see
// buildAsyncConsumerComponent).
func buildAsyncImportsAwaitOuter(inner []byte, imports []AsyncImportSpec, liftExportName string) []byte {
	n := len(imports)
	buf := PutComponentHeader(nil)
	var compc, compi, compf uint32

	// Nest + instantiate each provider; alias its async export → component func.
	provFunc := make([]uint32, n)
	argNames := make([]string, n)
	for i := range imports {
		buf = PutComponentSection(buf, imports[i].Provider)
		buf = PutInstanceSectionInstantiateComponent(buf, compc)
		compc++
		buf = PutAliasSectionInstanceExportFunc(buf, compi, imports[i].ProviderExportName)
		compi++
		provFunc[i] = compf
		compf++
		argNames[i] = fmt.Sprintf("dep%d", i)
	}

	// Nest the consumer component, instantiate it with the provider funcs.
	buf = PutComponentSection(buf, inner)
	consumerComp := compc
	compc++
	buf = PutInstanceSectionInstantiateComponentWithFuncArgs(buf, consumerComp, argNames, provFunc)
	consumerInst := compi
	compi++

	// Re-export the consumer's lifted async export.
	buf = PutAliasSectionInstanceExportFunc(buf, consumerInst, liftExportName)
	runFunc := compf
	compf++
	buf = PutExportSectionOneFunc(buf, liftExportName, runFunc)
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
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, cValtypeString)  // type 0: () -> string
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
	buf = PutTypeSectionOneFuncAsync(buf, []string{"s"}, []byte{cValtypeString}, cValtypeString)
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
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0)      // component type 1: () -> (type 0)
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
	buf = PutTypeSectionOneFuncAsync(buf, []string{"s"}, []byte{cValtypeString}, resultValtype) // component type 0
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
	buf = PutTypeSectionOneFuncGeneralAsync(buf, []string{"xs"}, [][]byte{leb128SlebBytes(0)}, []byte{resultValtype})
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
	buf = PutTypeSectionOneFuncGeneralAsync(buf, paramNames, paramVals, []byte{resultValtype})
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
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)                                  // type 0
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
	// Sibling composition (v46 — see buildAsyncConsumerComponent): the consumer
	// imports `dep0: async func() -> resultValtype`; the outer links a sibling
	// provider instance.
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype)       // component type 0 (import type)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{0}) // component func 0 (dep)

	// Externalized consumer memory → core memory 0.
	inner = PutCoreModuleSection(inner, memCore)                     // core module 0
	inner = PutCoreInstanceSectionInstantiate(inner, 0)              // core instance 0
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m") // core memory 0

	// Consumer canon glue (core funcs 0..6).
	inner = PutCanonTaskReturnSingle(inner, resultValtype) // core func 0 (tr)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)         // core func 1 (dl: lower dep async over memory 0)
	inner = PutCanonWaitableSetNew(inner)                  // core func 2 (wsn)
	inner = PutCanonWaitableJoin(inner)                    // core func 3 (wj)
	inner = PutCanonWaitableSetWait(inner, 0)              // core func 4 (wsw over memory 0)
	inner = PutCanonSubtaskDrop(inner)                     // core func 5 (sd)
	inner = PutCanonWaitableSetDrop(inner)                 // core func 6 (wsd)

	inner = PutCoreModuleSection(inner, consumerCore) // core module 1
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "dl", Sort: CoreSortFunc, Idx: 1},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 2},
		{Name: "wj", Sort: CoreSortFunc, Idx: 3},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 4},
		{Name: "sd", Sort: CoreSortFunc, Idx: 5},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 6},
	}) // core instance 1
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")             // core func 7
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype) // component type 1 (lift type)
	inner = PutCanonSectionLiftAsync(inner, 7, 1)                      // component func 1
	inner = PutExportSectionOneFunc(inner, "run", 1)

	provider := buildPendingProviderComponent(providerCore, resultValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: provider, ProviderExportName: "dep"}}, "run")
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
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype) // component type 1
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
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype) // component type 1
	buf = PutCanonSectionLiftAsync(buf, 4, 1)                 // component func 0
	buf = PutExportSectionOneFunc(buf, "run", 0)
	return buf
}

// buildFutureProducerComponent builds a nested sub-component
// `prod: async func() -> future<resultValtype>`: its core creates a future
// (future.new — readable=low32 / writable=high32), task.returns the readable end,
// then future.writes the value through the writable end. task.return of a future
// result is scalar (the readable handle), so it needs no memory; future.write
// reads the value from the producer's own memory, and since the producer core
// imports future.write the memory→instance→import dependency is circular — broken
// with the gMem trampoline (placeholder future.write → alias memory → real
// `future.write (mem)` → fixup the table slot), as BuildAsyncLiftedExportComponentString
// does for the string task.return. providerCore imports ("","tr"/"fnew"/"fw") and
// exports "mem" + "prod".
func buildFutureProducerComponent(producerCore []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeFuture(resultValtype)) // component type 0: future<T>

	// future.write trampoline (core sig (writable, ptr) -> status).
	fwParams, fwResults := []byte{0x7f, 0x7f}, []byte{0x7f}
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(fwParams, fwResults)) // core module 0
	buf = PutCoreInstanceSectionInstantiate(buf, 0)                                        // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")                                       // core func 0 (placeholder fw)

	buf = PutCanonTaskReturnTypeIdx(buf, 0) // core func 1 (tr; future type 0, no memory)
	buf = PutCanonFutureNew(buf, 0)         // core func 2 (fnew; future type 0)

	buf = PutCoreModuleSection(buf, producerCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 1},
		{Name: "fnew", Sort: CoreSortFunc, Idx: 2},
		{Name: "fw", Sort: CoreSortFunc, Idx: 0},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (producer)

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem") // core memory 0
	buf = PutCanonFutureWrite(buf, 0, 0)                           // core func 3 (real fw over memory 0)

	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(fwParams, fwResults)) // core module 2
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")                // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 3},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	buf = PutAliasSectionCoreExportFunc(buf, 2, "prod")    // core func 4 (prod)
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0) // component type 1: () -> future<T>(type 0)
	buf = PutCanonSectionLiftAsync(buf, 4, 1)              // component func 0
	buf = PutExportSectionOneFunc(buf, "prod", 0)
	return buf
}

// BuildFutureExportImportComponent assembles a runnable WASI Preview-3 component
// that passes a `future<resultValtype>` ACROSS a component boundary — the
// two-component split that exercises both future-ABI directions together. A
// nested producer (buildFutureProducerComponent) exports
// `prod: async func() -> future<T>`; the consumer `canon lower async`-es that
// import, reads the returned future readable handle from the lower's return area,
// `future.read`s the value, and re-returns it from its own async export `run`.
// The producer writes the value before yielding back (stackful), so both the
// prod-lower and the future.read complete synchronously (RETURNED) — no await
// loops. The consumer's memory is externalised into a shared core module so the
// lower / future.read memory options reference memory 0 without the
// lower→memory→instance circularity (the production path reuses the gMem
// trampoline; the producer's future.write IS trampolined because a nested
// component can't receive an external core memory). consumerCore must import
// ("","tr"/"prodl"/"fread") and ("mem","m"), and export "run". Proven to return
// the value under `wasmtime -W component-model-async,component-model-async-stackful`.
func BuildFutureExportImportComponent(producerCore, consumerCore, memCore []byte, resultValtype byte) []byte {
	// Sibling composition (v46 — see buildAsyncConsumerComponent): the consumer
	// imports `dep0: async func() -> future<T>`; the outer links a sibling
	// future-producer instance.
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneDefined(inner, InnerTypeFuture(resultValtype)) // component type 0: future<T>
	// component type 1: imported producer functype `async () -> future<T>`.
	inner = PutTypeSectionOneFuncResultIdxAsync(inner, nil, nil, 0)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{1}) // component func 0 (prod)

	// Externalised consumer memory → core memory 0.
	inner = PutCoreModuleSection(inner, memCore)                     // core module 0
	inner = PutCoreInstanceSectionInstantiate(inner, 0)              // core instance 0
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m") // core memory 0

	inner = PutCanonTaskReturnSingle(inner, resultValtype) // core func 0 (tr)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)         // core func 1 (prodl: lower prod over memory 0)
	inner = PutCanonFutureRead(inner, 0, 0)                // core func 2 (fread: future type 0 over memory 0)

	inner = PutCoreModuleSection(inner, consumerCore) // core module 1
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "prodl", Sort: CoreSortFunc, Idx: 1},
		{Name: "fread", Sort: CoreSortFunc, Idx: 2},
	}) // core instance 1
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")             // core func 3
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype) // component type 2: () -> T
	inner = PutCanonSectionLiftAsync(inner, 3, 2)                      // component func 1
	inner = PutExportSectionOneFunc(inner, "run", 1)

	producer := buildFutureProducerComponent(producerCore, resultValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, "run")
}

// buildStreamProducerComponent builds a nested sub-component
// `prod: async func() -> stream<elem>`: its core creates a stream (stream.new —
// readable=low32 / writable=high32), task.returns the readable end, then
// stream.writes `count` elements through the writable end. Like the future
// producer, stream.write reads from the producer's own memory and the producer
// core imports it, so the memory option is circular — broken with the gMem
// trampoline. providerCore imports ("","tr"/"snew"/"sw") and exports "mem" + "prod".
func buildStreamProducerComponent(producerCore []byte, elemValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype)) // component type 0: stream<elem>

	// stream.write trampoline (core sig (writable, ptr, count) -> status).
	swParams, swResults := []byte{0x7f, 0x7f, 0x7f}, []byte{0x7f}
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(swParams, swResults)) // core module 0
	buf = PutCoreInstanceSectionInstantiate(buf, 0)                                        // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")                                       // core func 0 (placeholder sw)

	buf = PutCanonTaskReturnTypeIdx(buf, 0) // core func 1 (tr; stream type 0, no memory)
	buf = PutCanonStreamNew(buf, 0)         // core func 2 (snew; stream type 0)

	buf = PutCoreModuleSection(buf, producerCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 1},
		{Name: "snew", Sort: CoreSortFunc, Idx: 2},
		{Name: "sw", Sort: CoreSortFunc, Idx: 0},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (producer)

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem") // core memory 0
	buf = PutCanonStreamWrite(buf, 0, 0)                           // core func 3 (real sw over memory 0)

	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(swParams, swResults)) // core module 2
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")                // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 3},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	buf = PutAliasSectionCoreExportFunc(buf, 2, "prod")    // core func 4 (prod)
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0) // component type 1: () -> stream<elem>(type 0)
	buf = PutCanonSectionLiftAsync(buf, 4, 1)              // component func 0
	buf = PutExportSectionOneFunc(buf, "prod", 0)
	return buf
}

// BuildStreamExportImportComponent assembles a runnable WASI Preview-3 component
// that passes a `stream<elem>` ACROSS a component boundary — the stream
// counterpart of BuildFutureExportImportComponent. A nested producer
// (buildStreamProducerComponent) exports `prod: async func() -> stream<elem>`
// (stream.new → task.return the readable end → stream.write the elements); the
// consumer `canon lower async`-es that import, reads the returned stream readable
// handle, `stream.read`s the elements, derives a scalar, and re-returns it from
// its async export `run`. The producer's write buffers across the task boundary,
// so the consumer's later read drains it synchronously — no await loop.
// consumerCore must import ("","tr"/"prodl"/"sread") and ("mem","m"), and export
// "run". Proven to return its value under `wasmtime -W
// component-model-async,component-model-async-stackful`.
func BuildStreamExportImportComponent(producerCore, consumerCore, memCore []byte, elemValtype, resultValtype byte) []byte {
	// Sibling composition (v46 — see buildAsyncConsumerComponent): the consumer
	// imports `dep0: async func() -> stream<elem>`; the outer links a sibling
	// stream-producer instance.
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneDefined(inner, InnerTypeStream(elemValtype)) // component type 0: stream<elem>
	// component type 1: imported producer functype `async () -> stream<elem>`.
	inner = PutTypeSectionOneFuncResultIdxAsync(inner, nil, nil, 0)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{1}) // component func 0 (prod)

	// Externalised consumer memory → core memory 0.
	inner = PutCoreModuleSection(inner, memCore)                     // core module 0
	inner = PutCoreInstanceSectionInstantiate(inner, 0)              // core instance 0
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m") // core memory 0

	inner = PutCanonTaskReturnSingle(inner, resultValtype) // core func 0 (tr)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)         // core func 1 (prodl: lower prod over memory 0)
	inner = PutCanonStreamRead(inner, 0, 0)                // core func 2 (sread: stream type 0 over memory 0)

	inner = PutCoreModuleSection(inner, consumerCore) // core module 1
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "prodl", Sort: CoreSortFunc, Idx: 1},
		{Name: "sread", Sort: CoreSortFunc, Idx: 2},
	}) // core instance 1
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")             // core func 3
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype) // component type 2: () -> resultValtype
	inner = PutCanonSectionLiftAsync(inner, 3, 2)                      // component func 1
	inner = PutExportSectionOneFunc(inner, "run", 1)

	producer := buildStreamProducerComponent(producerCore, elemValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, "run")
}
