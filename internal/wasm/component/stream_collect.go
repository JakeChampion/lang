package component

// stream_collect.go assembles a runnable WASI Preview-3 component that delivers a
// `stream<elem>` ACROSS a component boundary and COLLECTS it to completion — the
// composer half of the colorless `stream[T]` surface (docs/STREAM-TYPE-SURFACE.md).
// A nested producer exports `prod: async func() -> stream<elem>` and writes its
// elements then drops the writable end (EOF); the consumer lowers that import,
// reads the returned readable handle in a collect loop (stream.read + the await
// loop, growing an array until CLOSED), and re-returns a scalar derived from the
// collected elements. It is the two-sided, await-driven counterpart of
// BuildStreamExportImportComponent (which read a fixed count): here the producer
// write-awaits before dropping, and the consumer collects to EOF.
//
// Both await sides need a `memory` option (waitable-set.wait, stream.read/write
// stage bytes through memory). The producer references its OWN memory — circular
// because it imports those canon funcs — so its stream.write + waitable-set.wait
// go through a 2-slot gMem trampoline + fixup. The consumer uses an externalised
// shared memory (the spike form; the production path with a wasmbin-generated
// consumer that exports its own memory reuses the per-import trampolines of
// BuildAsyncImportsAwaitComponent).

// buildStreamEOFProducerComponent builds the nested `prod: async func() ->
// stream<elem>` sub-component whose core (producerCore) writes its elements,
// AWAITS the write (a write with no reader BLOCKs; dropping a busy writable
// traps), then `stream.drop-writable`s to signal EOF. producerCore imports under
// "" : tr (task.return(stream), (handle)->()), snew (stream.new ()->i64), sw
// (stream.write (wr,ptr,cnt)->status), sdw (stream.drop-writable (wr)->()), wsn
// (waitable-set.new ()->i32), wj (waitable.join (w,set)->()), wsw
// (waitable-set.wait (set,ptr)->i32), wsd (waitable-set.drop (set)->()); exports
// "mem" + "prod". stream.write + waitable-set.wait reference the producer memory,
// so they are 2-slot-trampolined (slot 0 = write, slot 1 = wait).
func buildStreamEOFProducerComponent(producerCore []byte, tramp2, fixup2 []byte, elemValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype)) // component type 0: stream<elem>

	// 2-slot trampoline (write@0, wait@1) → core instance 0; placeholder funcs.
	buf = PutCoreModuleSection(buf, tramp2)           // core module 0
	buf = PutCoreInstanceSectionInstantiate(buf, 0)   // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "w")  // core func 0 (write placeholder)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "wt") // core func 1 (wait placeholder)

	// Memory-independent canon glue → direct core funcs.
	buf = PutCanonTaskReturnTypeIdx(buf, 0)  // core func 2 (tr; stream type 0)
	buf = PutCanonStreamNew(buf, 0)          // core func 3 (snew)
	buf = PutCanonStreamDropWritable(buf, 0) // core func 4 (sdw)
	buf = PutCanonWaitableSetNew(buf)        // core func 5 (wsn)
	buf = PutCanonWaitableJoin(buf)          // core func 6 (wj)
	buf = PutCanonWaitableSetDrop(buf)       // core func 7 (wsd)

	// Producer core, wired to the glue (sw/wsw via the trampoline placeholders).
	buf = PutCoreModuleSection(buf, producerCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 2},
		{Name: "snew", Sort: CoreSortFunc, Idx: 3},
		{Name: "sw", Sort: CoreSortFunc, Idx: 0},
		{Name: "sdw", Sort: CoreSortFunc, Idx: 4},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 5},
		{Name: "wj", Sort: CoreSortFunc, Idx: 6},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 1},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 7},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (producer)

	// Alias producer memory → core memory 0; real stream.write + waitable-set.wait over it.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem") // core memory 0
	buf = PutCanonStreamWrite(buf, 0, 0)                           // core func 8 (real sw)
	buf = PutCanonWaitableSetWait(buf, 0)                          // core func 9 (real wsw)

	// Fixup module 2: patch trampoline table slot 0 → write(8), slot 1 → wait(9).
	buf = PutCoreModuleSection(buf, fixup2)                            // core module 2
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports") // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 8},
		{Name: "1", Sort: CoreSortFunc, Idx: 9},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	buf = PutAliasSectionCoreExportFunc(buf, 2, "prod")         // core func 10 (prod)
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0) // component type 1: () -> stream<elem>(type 0)
	buf = PutCanonSectionLiftAsync(buf, 10, 1)                  // component func 0
	buf = PutExportSectionOneFunc(buf, "prod", 0)
	return buf
}

// BuildStreamCollectComponent wraps a consumer core that COLLECTS a `stream<elem>`
// async import to completion into a runnable component. The nested producer
// (buildStreamEOFProducerComponent) is lowered with `canon lower async` over the
// externalised shared memory; the consumer reads the returned stream readable
// handle in a collect loop and re-returns a scalar (`run: async func() ->
// resultValtype`). consumerCore must import under "" : tr (task.return scalar),
// prodl (the prod lower, (retptr)->status), sr (stream.read), sdr
// (stream.drop-readable), wsn/wj/wsw/wsd; and ("mem","m"); and export "run".
// Proven to return its value under `wasmtime -W
// component-model-async,component-model-async-stackful`.
func BuildStreamCollectComponent(producerCore, consumerCore, memCore, tramp2, fixup2 []byte, elemValtype, resultValtype byte) []byte {
	// Sibling composition (v46 — see buildAsyncConsumerComponent): the consumer
	// half is a nested component importing `dep0: async func() -> stream<elem>`;
	// the outer links a sibling EOF-producer instance.
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneDefined(inner, InnerTypeStream(elemValtype)) // component type 0: stream<elem>
	// component type 1: imported producer functype `async () -> stream<elem>`.
	inner = PutTypeSectionOneFuncResultIdxAsync(inner, nil, nil, 0)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{1}) // component func 0 (prod)

	// Externalised consumer memory → core memory 0.
	inner = PutCoreModuleSection(inner, memCore)                     // core module 0
	inner = PutCoreInstanceSectionInstantiate(inner, 0)              // core instance 0
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m") // core memory 0

	// Consumer canon glue over memory 0 (externalised — no consumer-side trampolines).
	inner = PutCanonTaskReturnSingle(inner, resultValtype) // core func 0 (tr)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)         // core func 1 (prodl: lower prod over mem 0)
	inner = PutCanonStreamRead(inner, 0, 0)                // core func 2 (sr; stream type 0 over mem 0)
	inner = PutCanonStreamDropReadable(inner, 0)           // core func 3 (sdr)
	inner = PutCanonWaitableSetNew(inner)                  // core func 4 (wsn)
	inner = PutCanonWaitableJoin(inner)                    // core func 5 (wj)
	inner = PutCanonWaitableSetWait(inner, 0)              // core func 6 (wsw over mem 0)
	inner = PutCanonWaitableSetDrop(inner)                 // core func 7 (wsd)

	inner = PutCoreModuleSection(inner, consumerCore) // core module 1
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "prodl", Sort: CoreSortFunc, Idx: 1},
		{Name: "sr", Sort: CoreSortFunc, Idx: 2},
		{Name: "sdr", Sort: CoreSortFunc, Idx: 3},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 4},
		{Name: "wj", Sort: CoreSortFunc, Idx: 5},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 6},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 7},
	}) // core instance 1
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (consumer)

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")             // core func 8
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype) // component type 2: () -> resultValtype
	inner = PutCanonSectionLiftAsync(inner, 8, 2)                      // component func 1
	inner = PutExportSectionOneFunc(inner, "run", 1)

	producer := buildStreamEOFProducerComponent(producerCore, tramp2, fixup2, elemValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, "run")
}

// BuildAsyncStreamImportComponent is the real-Fern integration of the colorless
// stream[T] collect path: it wraps a WASMBIN-GENERATED consumer core (which
// exports its own memory and whose `stream[T]` async import resolves to the
// collect-wrapper — buildExternAsyncStreamResultWrapper) against a bundled EOF
// producer. Unlike BuildStreamCollectComponent (externalised consumer memory for
// the spike), here the consumer's memory is exported and aliased only after
// instantiation, so the three memory-carrying canon funcs the consumer imports —
// the dep-lower `(retptr)->status`, `waitable-set.wait`, and `stream.read` — each
// go through their own gMem trampoline + fixup (the BuildAsyncImportsAwaitComponent
// pattern, plus the stream.read trampoline). The memory-independent glue
// (task.return, ws-new/w-join/subtask-drop/ws-drop, stream.drop-readable) is
// emitted directly. The EOF producer is nested (buildStreamEOFProducerComponent).
// consumerAsyncExport is lifted async as liftExportName. See
// docs/STREAM-TYPE-SURFACE.md.
func BuildAsyncStreamImportComponent(consumerCore, producerCore, tramp2, fixup2 []byte,
	importIface, importWITName, consumerAsyncExport, liftExportName string,
	elemValtype, resultValtype byte) []byte {
	// Sibling composition (required on wasmtime v46 — see
	// buildAsyncConsumerComponent): the consumer machinery is a nested
	// component importing the producer's `dep0: async func() -> stream<elem>`,
	// and the outer links a sibling EOF-producer instance to it.
	inner := buildStreamImportConsumerComponent(consumerCore, importIface, importWITName, consumerAsyncExport, liftExportName, elemValtype, resultValtype)
	producer := buildStreamEOFProducerComponent(producerCore, tramp2, fixup2, elemValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, liftExportName)
}

func buildStreamImportConsumerComponent(consumerCore []byte,
	importIface, importWITName, consumerAsyncExport, liftExportName string,
	elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	var cf, ci, cm, ct, compf, compType uint32

	// Component type 0: stream<elem> (referenced by stream.read + the import type).
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype))
	compType++ // stream type 0

	// Component type 1: the imported producer functype `async () -> stream<elem>`
	// (result references stream type 0), imported as component func depCFunc.
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0)
	importTypeIdx := compType
	compType++
	buf = PutComponentImportSectionFuncs(buf, []string{"dep0"}, []uint32{importTypeIdx})
	depCFunc := compf
	compf++

	// Phase 2: trampolines for the three consumer-memory-carrying funcs.
	lowerParams, lowerResults := []byte{0x7f}, []byte{0x7f} // dep-lower (retptr)->status
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(lowerParams, lowerResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	depTrampInst := ci
	ci++
	wsWaitParams, wsWaitResults := []byte{0x7f, 0x7f}, []byte{0x7f} // ws-wait (set,evt)->i32
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(wsWaitParams, wsWaitResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	wsWaitTrampInst := ci
	ci++
	srParams, srResults := []byte{0x7f, 0x7f, 0x7f}, []byte{0x7f} // stream.read (rd,ptr,cnt)->i32
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(srParams, srResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	srTrampInst := ci
	ci++

	// Phase 3: memory-independent canon glue → direct core funcs.
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
	sdrCoreF := cf
	buf = PutCanonStreamDropReadable(buf, 0) // stream type 0
	cf++

	// Phase 4: placeholders aliased out of the trampolines.
	buf = PutAliasSectionCoreExportFunc(buf, depTrampInst, "0")
	depPlaceholderF := cf
	cf++
	buf = PutAliasSectionCoreExportFunc(buf, wsWaitTrampInst, "0")
	wsWaitPlaceholderF := cf
	cf++
	buf = PutAliasSectionCoreExportFunc(buf, srTrampInst, "0")
	srPlaceholderF := cf
	cf++

	// Phase 5: consumer core module.
	buf = PutCoreModuleSection(buf, consumerCore)
	consumerMod := cm
	cm++

	// Phase 6: "" glue instance + import-iface instance + consumer instantiation.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: trCoreF},
		{Name: "ws-new", Sort: CoreSortFunc, Idx: wsNewCoreF},
		{Name: "w-join", Sort: CoreSortFunc, Idx: wJoinCoreF},
		{Name: "ws-wait", Sort: CoreSortFunc, Idx: wsWaitPlaceholderF},
		{Name: "subtask-drop", Sort: CoreSortFunc, Idx: subtaskDropCoreF},
		{Name: "ws-drop", Sort: CoreSortFunc, Idx: wsDropCoreF},
		{Name: "stream-read", Sort: CoreSortFunc, Idx: srPlaceholderF},
		{Name: "stream-drop-readable", Sort: CoreSortFunc, Idx: sdrCoreF},
	})
	emptyInst := ci
	ci++
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: importWITName, Sort: CoreSortFunc, Idx: depPlaceholderF},
	})
	importInst := ci
	ci++
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, consumerMod,
		[]string{"", importIface}, []uint32{emptyInst, importInst})
	consumerInst := ci
	ci++

	// Phase 7: alias the consumer memory → core memory 0.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, consumerInst, "memory")
	mem := uint32(0)

	// Phase 8: the real dep-lower / ws-wait / stream-read over the consumer memory.
	buf = PutCanonSectionLowerAsync(buf, depCFunc, mem)
	depRealF := cf
	cf++
	buf = PutCanonWaitableSetWait(buf, mem)
	wsWaitRealF := cf
	cf++
	buf = PutCanonStreamRead(buf, 0, mem) // stream type 0
	srRealF := cf
	cf++

	// Phase 9: fixup each trampoline table slot 0 → its real func.
	fixup := func(trampInst, realF uint32, p, r []byte) {
		buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(p, r))
		fixupMod := cm
		cm++
		buf = PutAliasSectionCoreExport(buf, CoreSortTable, trampInst, "$imports")
		tbl := ct
		ct++
		buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
			{Name: "$imports", Sort: CoreSortTable, Idx: tbl},
			{Name: "0", Sort: CoreSortFunc, Idx: realF},
		})
		argInst := ci
		ci++
		buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, fixupMod, []string{""}, []uint32{argInst})
		ci++
	}
	fixup(depTrampInst, depRealF, lowerParams, lowerResults)
	fixup(wsWaitTrampInst, wsWaitRealF, wsWaitParams, wsWaitResults)
	fixup(srTrampInst, srRealF, srParams, srResults)

	// Phase 10: lift the consumer's async export (functype at component type
	// index compType, after the stream + import functypes).
	buf = PutAliasSectionCoreExportFunc(buf, consumerInst, consumerAsyncExport)
	runCoreF := cf
	cf++
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)
	buf = PutCanonSectionLiftAsync(buf, runCoreF, compType)
	compType++
	liftCompF := compf
	compf++
	buf = PutExportSectionOneFunc(buf, liftExportName, liftCompF)
	return buf
}

// buildStreamSinkProviderComponent builds the nested host `sink: async func(s:
// stream<elem>) -> u32` sub-component whose core (sinkCore) COLLECT-READS the
// stream param to EOF and task-returns a scalar — the read-side mirror of
// buildStreamEOFProducerComponent. sinkCore imports under "" : tr (task.return
// scalar), sr (stream.read), sdr (stream.drop-readable), wsn/wj/wsw/wsd; exports
// "mem" + "sink". stream.read + waitable-set.wait reference its own memory and it
// imports them, so they go through the same 2-slot gMem trampoline (slot 0 = read,
// slot 1 = wait) + fixup.
func buildStreamSinkProviderComponent(sinkCore []byte, tramp2, fixup2 []byte, elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype)) // component type 0: stream<elem>

	buf = PutCoreModuleSection(buf, tramp2)           // core module 0 (2-slot tramp: read@0, wait@1)
	buf = PutCoreInstanceSectionInstantiate(buf, 0)   // core instance 0
	buf = PutAliasSectionCoreExportFunc(buf, 0, "w")  // core func 0 (read placeholder, slot 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "wt") // core func 1 (wait placeholder, slot 1)

	buf = PutCanonTaskReturnSingle(buf, resultValtype) // core func 2 (tr; scalar)
	buf = PutCanonStreamDropReadable(buf, 0)           // core func 3 (sdr)
	buf = PutCanonWaitableSetNew(buf)                  // core func 4 (wsn)
	buf = PutCanonWaitableJoin(buf)                    // core func 5 (wj)
	buf = PutCanonWaitableSetDrop(buf)                 // core func 6 (wsd)

	buf = PutCoreModuleSection(buf, sinkCore) // core module 1
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 2},
		{Name: "sr", Sort: CoreSortFunc, Idx: 0},
		{Name: "sdr", Sort: CoreSortFunc, Idx: 3},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 4},
		{Name: "wj", Sort: CoreSortFunc, Idx: 5},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 1},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 6},
	}) // core instance 1
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1}) // core instance 2 (sink)

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem") // core memory 0
	buf = PutCanonStreamRead(buf, 0, 0)                            // core func 7 (real sr over memory 0)
	buf = PutCanonWaitableSetWait(buf, 0)                          // core func 8 (real wsw over memory 0)

	buf = PutCoreModuleSection(buf, fixup2)                            // core module 2
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports") // core table 0
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 7},
		{Name: "1", Sort: CoreSortFunc, Idx: 8},
	}) // core instance 3
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3}) // core instance 4 (fixup)

	buf = PutAliasSectionCoreExportFunc(buf, 2, "sink") // core func 9 (sink)
	// component type 1: func(s: stream<elem>(type 0)) -> resultValtype.
	buf = PutTypeSectionOneFuncGeneralAsync(buf, []string{"s"}, [][]byte{leb128SlebBytes(0)}, []byte{resultValtype})
	buf = PutCanonSectionLiftAsync(buf, 9, 1) // component func 0
	buf = PutExportSectionOneFunc(buf, "sink", 0)
	return buf
}

// BuildAsyncStreamParamImportComponent is the real-Fern integration of the
// colorless stream[T] PRODUCE path — the mirror of BuildAsyncStreamImportComponent.
// It wraps a WASMBIN-GENERATED consumer (which exports its own memory and whose
// `stream[T]` async-import param resolves to the produce-wrapper) against a
// bundled host sink that collect-reads the stream. The consumer's three
// memory-carrying imports — the dep-lower `(rd, retptr)->status`,
// `waitable-set.wait`, and `stream.write` — each go through their own gMem
// trampoline + fixup over the consumer's exported memory; the memory-independent
// glue (task.return, ws-new/w-join/subtask-drop/ws-drop, stream.new,
// stream.drop-writable) is direct. See docs/STREAM-TYPE-SURFACE.md.
func BuildAsyncStreamParamImportComponent(consumerCore, sinkCore, tramp2, fixup2 []byte,
	importIface, importWITName, consumerAsyncExport, liftExportName string,
	elemValtype, resultValtype byte) []byte {
	// Sibling composition (v46 — see buildAsyncConsumerComponent): the consumer
	// imports `dep0: async func(s: stream<elem>) -> result`; the outer links a
	// sibling host-sink instance.
	inner := buildStreamParamImportConsumerComponent(consumerCore, importIface, importWITName, consumerAsyncExport, liftExportName, elemValtype, resultValtype)
	sink := buildStreamSinkProviderComponent(sinkCore, tramp2, fixup2, elemValtype, resultValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: sink, ProviderExportName: "sink"}}, liftExportName)
}

func buildStreamParamImportConsumerComponent(consumerCore []byte,
	importIface, importWITName, consumerAsyncExport, liftExportName string,
	elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	var cf, ci, cm, ct, compf, compType uint32

	// Component type 0: stream<elem> (referenced by stream.write + the import).
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype))
	compType++ // stream type 0

	// Component type 1: the imported sink functype `async func(s: stream<elem>)
	// -> result` (param references stream type 0); imported as func depCFunc.
	buf = PutTypeSectionOneFuncGeneralAsync(buf, []string{"s"}, [][]byte{leb128SlebBytes(0)}, []byte{resultValtype})
	importTypeIdx := compType
	compType++
	buf = PutComponentImportSectionFuncs(buf, []string{"dep0"}, []uint32{importTypeIdx})
	depCFunc := compf
	compf++

	// Phase 2: trampolines for the three consumer-memory-carrying funcs.
	lowerParams, lowerResults := []byte{0x7f, 0x7f}, []byte{0x7f} // dep-lower (rd,retptr)->status
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(lowerParams, lowerResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	depTrampInst := ci
	ci++
	wsWaitParams, wsWaitResults := []byte{0x7f, 0x7f}, []byte{0x7f} // ws-wait (set,evt)->i32
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(wsWaitParams, wsWaitResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	wsWaitTrampInst := ci
	ci++
	swParams, swResults := []byte{0x7f, 0x7f, 0x7f}, []byte{0x7f} // stream.write (wr,ptr,cnt)->i32
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(swParams, swResults))
	cm++
	buf = PutCoreInstanceSectionInstantiate(buf, cm-1)
	swTrampInst := ci
	ci++

	// Phase 3: memory-independent canon glue → direct core funcs.
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
	snewCoreF := cf
	buf = PutCanonStreamNew(buf, 0) // stream type 0
	cf++
	sdwCoreF := cf
	buf = PutCanonStreamDropWritable(buf, 0) // stream type 0
	cf++

	// Phase 4: placeholders aliased out of the trampolines.
	buf = PutAliasSectionCoreExportFunc(buf, depTrampInst, "0")
	depPlaceholderF := cf
	cf++
	buf = PutAliasSectionCoreExportFunc(buf, wsWaitTrampInst, "0")
	wsWaitPlaceholderF := cf
	cf++
	buf = PutAliasSectionCoreExportFunc(buf, swTrampInst, "0")
	swPlaceholderF := cf
	cf++

	// Phase 5: consumer core module.
	buf = PutCoreModuleSection(buf, consumerCore)
	consumerMod := cm
	cm++

	// Phase 6: "" glue instance + import-iface instance + consumer instantiation.
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: trCoreF},
		{Name: "ws-new", Sort: CoreSortFunc, Idx: wsNewCoreF},
		{Name: "w-join", Sort: CoreSortFunc, Idx: wJoinCoreF},
		{Name: "ws-wait", Sort: CoreSortFunc, Idx: wsWaitPlaceholderF},
		{Name: "subtask-drop", Sort: CoreSortFunc, Idx: subtaskDropCoreF},
		{Name: "ws-drop", Sort: CoreSortFunc, Idx: wsDropCoreF},
		{Name: "stream-new", Sort: CoreSortFunc, Idx: snewCoreF},
		{Name: "stream-write", Sort: CoreSortFunc, Idx: swPlaceholderF},
		{Name: "stream-drop-writable", Sort: CoreSortFunc, Idx: sdwCoreF},
	})
	emptyInst := ci
	ci++
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: importWITName, Sort: CoreSortFunc, Idx: depPlaceholderF},
	})
	importInst := ci
	ci++
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, consumerMod,
		[]string{"", importIface}, []uint32{emptyInst, importInst})
	consumerInst := ci
	ci++

	// Phase 7: alias the consumer memory → core memory 0.
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, consumerInst, "memory")
	mem := uint32(0)

	// Phase 8: the real dep-lower / ws-wait / stream-write over the consumer memory.
	buf = PutCanonSectionLowerAsync(buf, depCFunc, mem)
	depRealF := cf
	cf++
	buf = PutCanonWaitableSetWait(buf, mem)
	wsWaitRealF := cf
	cf++
	buf = PutCanonStreamWrite(buf, 0, mem) // stream type 0
	swRealF := cf
	cf++

	// Phase 9: fixup each trampoline table slot 0 → its real func.
	fixup := func(trampInst, realF uint32, p, r []byte) {
		buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(p, r))
		fixupMod := cm
		cm++
		buf = PutAliasSectionCoreExport(buf, CoreSortTable, trampInst, "$imports")
		tbl := ct
		ct++
		buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
			{Name: "$imports", Sort: CoreSortTable, Idx: tbl},
			{Name: "0", Sort: CoreSortFunc, Idx: realF},
		})
		argInst := ci
		ci++
		buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, fixupMod, []string{""}, []uint32{argInst})
		ci++
	}
	fixup(depTrampInst, depRealF, lowerParams, lowerResults)
	fixup(wsWaitTrampInst, wsWaitRealF, wsWaitParams, wsWaitResults)
	fixup(swTrampInst, swRealF, swParams, swResults)

	// Phase 10: lift the consumer's async export (functype at type index compType).
	buf = PutAliasSectionCoreExportFunc(buf, consumerInst, consumerAsyncExport)
	runCoreF := cf
	cf++
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)
	buf = PutCanonSectionLiftAsync(buf, runCoreF, compType)
	compType++
	liftCompF := compf
	compf++
	buf = PutExportSectionOneFunc(buf, liftExportName, liftCompF)
	return buf
}
