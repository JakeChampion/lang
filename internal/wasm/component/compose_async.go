package component

import "fmt"

type AsyncImportSpec struct {
	Iface, WITName      string
	Provider            []byte
	ProviderExportName  string
	LowerParams         []byte
	LowerResults        []byte
	NeedsRealloc        bool
	ImportResultValtype byte
	ImportParamNames    []string
	ImportParamVals     [][]byte
	ImportResultVal     []byte
	ImportDefinedTypes  [][]byte
}

func buildAsyncImportsAwaitOuter(inner []byte, imports []AsyncImportSpec, liftExportName string) []byte {
	n := len(imports)
	buf := PutComponentHeader(nil)
	var compc, compi, compf uint32

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

	buf = PutComponentSection(buf, inner)
	consumerComp := compc
	compc++
	buf = PutInstanceSectionInstantiateComponentWithFuncArgs(buf, consumerComp, argNames, provFunc)
	consumerInst := compi
	compi++

	buf = PutAliasSectionInstanceExportFunc(buf, consumerInst, liftExportName)
	runFunc := compf
	compf++
	buf = PutExportSectionOneFunc(buf, liftExportName, runFunc)
	return buf
}

func BuildAsyncLiftedExportComponentString(providerCore []byte, coreMemExportName, coreExportName, exportName string) []byte {
	trParams := []byte{0x7f, 0x7f}
	buf := PutComponentHeader(nil)

	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(trParams, nil))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")

	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, coreMemExportName)
	buf = PutCanonTaskReturnStringWithMemory(buf, 0)

	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(trParams, nil))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 1},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3})

	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName)
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, cValtypeString)
	buf = PutCanonSectionLiftAsyncWithMemory(buf, 2, 0, 0)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

func BuildAsyncLiftedExportComponentList(providerCore []byte, coreMemExportName, coreExportName, exportName string, elemValtype byte) []byte {
	trParams := []byte{0x7f, 0x7f}
	buf := PutComponentHeader(nil)

	buf = PutTypeSectionOneDefined(buf, InnerTypeList(elemValtype))

	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(trParams, nil))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")

	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "task-return", Sort: CoreSortFunc, Idx: 0},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, coreMemExportName)
	buf = PutCanonTaskReturnTypeIdxWithMemory(buf, 0, 0)

	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(trParams, nil))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 1},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3})

	buf = PutAliasSectionCoreExportFunc(buf, 2, coreExportName)
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0)
	buf = PutCanonSectionLiftAsyncWithMemory(buf, 2, 1, 0)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

func BuildAsyncLiftedExportComponentStringParam(providerCore []byte, memExportName, reallocExportName, coreExportName, exportName string, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 1, memExportName)
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 1, reallocExportName)
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)
	buf = PutTypeSectionOneFuncAsync(buf, []string{"s"}, []byte{cValtypeString}, resultValtype)
	buf = PutCanonSectionLiftAsyncWithMemoryRealloc(buf, 2, 0, 0, 1)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

func BuildAsyncLiftedExportComponentListParam(providerCore []byte, memExportName, reallocExportName, coreExportName, exportName string, elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	buf = PutTypeSectionOneDefined(buf, InnerTypeList(elemValtype))

	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 1, memExportName)
	buf = PutAliasSectionCoreExport(buf, CoreSortFunc, 1, reallocExportName)
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)
	buf = PutTypeSectionOneFuncGeneralAsync(buf, []string{"xs"}, [][]byte{leb128SlebBytes(0)}, []byte{resultValtype})
	buf = PutCanonSectionLiftAsyncWithMemoryRealloc(buf, 2, 1, 0, 1)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

func buildPendingProviderComponent(providerCore []byte, resultValtype byte) []byte {
	return BuildPendingDeferringProviderComponent(providerCore, "dep", "dep", resultValtype)
}

func BuildPendingDeferringProviderComponent(providerCore []byte, coreExportName, exportName string, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutCanonThreadYield(buf)
	buf = PutCoreModuleSection(buf, providerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "y", Sort: CoreSortFunc, Idx: 1},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0})
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)
	buf = PutCanonSectionLiftAsync(buf, 2, 0)
	buf = PutExportSectionOneFunc(buf, exportName, 0)
	return buf
}

func BuildPendingAwaitComponent(providerCore, consumerCore, memCore []byte, resultValtype byte) []byte {
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{0})

	inner = PutCoreModuleSection(inner, memCore)
	inner = PutCoreInstanceSectionInstantiate(inner, 0)
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m")

	inner = PutCanonTaskReturnSingle(inner, resultValtype)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)
	inner = PutCanonWaitableSetNew(inner)
	inner = PutCanonWaitableJoin(inner)
	inner = PutCanonWaitableSetWait(inner, 0)
	inner = PutCanonSubtaskDrop(inner)
	inner = PutCanonWaitableSetDrop(inner)

	inner = PutCoreModuleSection(inner, consumerCore)
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "dl", Sort: CoreSortFunc, Idx: 1},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 2},
		{Name: "wj", Sort: CoreSortFunc, Idx: 3},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 4},
		{Name: "sd", Sort: CoreSortFunc, Idx: 5},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 6},
	})
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0})

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype)
	inner = PutCanonSectionLiftAsync(inner, 7, 1)
	inner = PutExportSectionOneFunc(inner, "run", 1)

	provider := buildPendingProviderComponent(providerCore, resultValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: provider, ProviderExportName: "dep"}}, "run")
}

func BuildFutureRoundtripComponent(consumerCore, memCore []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	buf = PutTypeSectionOneDefined(buf, InnerTypeFuture(resultValtype))

	buf = PutCoreModuleSection(buf, memCore)
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 0, "m")

	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutCanonFutureNew(buf, 0)
	buf = PutCanonFutureWrite(buf, 0, 0)
	buf = PutCanonFutureRead(buf, 0, 0)

	buf = PutCoreModuleSection(buf, consumerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "fnew", Sort: CoreSortFunc, Idx: 1},
		{Name: "fwrite", Sort: CoreSortFunc, Idx: 2},
		{Name: "fread", Sort: CoreSortFunc, Idx: 3},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{"", "mem"}, []uint32{1, 0})

	buf = PutAliasSectionCoreExportFunc(buf, 2, "run")
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)
	buf = PutCanonSectionLiftAsync(buf, 4, 1)
	buf = PutExportSectionOneFunc(buf, "run", 0)
	return buf
}

func BuildStreamRoundtripComponent(consumerCore, memCore []byte, elemValtype, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)

	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype))

	buf = PutCoreModuleSection(buf, memCore)
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 0, "m")

	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutCanonStreamNew(buf, 0)
	buf = PutCanonStreamWrite(buf, 0, 0)
	buf = PutCanonStreamRead(buf, 0, 0)

	buf = PutCoreModuleSection(buf, consumerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "snew", Sort: CoreSortFunc, Idx: 1},
		{Name: "swrite", Sort: CoreSortFunc, Idx: 2},
		{Name: "sread", Sort: CoreSortFunc, Idx: 3},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{"", "mem"}, []uint32{1, 0})

	buf = PutAliasSectionCoreExportFunc(buf, 2, "run")
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)
	buf = PutCanonSectionLiftAsync(buf, 4, 1)
	buf = PutExportSectionOneFunc(buf, "run", 0)
	return buf
}

func buildFutureProducerComponent(producerCore []byte, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeFuture(resultValtype))

	fwParams, fwResults := []byte{0x7f, 0x7f}, []byte{0x7f}
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(fwParams, fwResults))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")

	buf = PutCanonTaskReturnTypeIdx(buf, 0)
	buf = PutCanonFutureNew(buf, 0)

	buf = PutCoreModuleSection(buf, producerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 1},
		{Name: "fnew", Sort: CoreSortFunc, Idx: 2},
		{Name: "fw", Sort: CoreSortFunc, Idx: 0},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem")
	buf = PutCanonFutureWrite(buf, 0, 0)

	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(fwParams, fwResults))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 3},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3})

	buf = PutAliasSectionCoreExportFunc(buf, 2, "prod")
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0)
	buf = PutCanonSectionLiftAsync(buf, 4, 1)
	buf = PutExportSectionOneFunc(buf, "prod", 0)
	return buf
}

func BuildFutureExportImportComponent(producerCore, consumerCore, memCore []byte, resultValtype byte) []byte {
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneDefined(inner, InnerTypeFuture(resultValtype))
	inner = PutTypeSectionOneFuncResultIdxAsync(inner, nil, nil, 0)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{1})

	inner = PutCoreModuleSection(inner, memCore)
	inner = PutCoreInstanceSectionInstantiate(inner, 0)
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m")

	inner = PutCanonTaskReturnSingle(inner, resultValtype)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)
	inner = PutCanonFutureRead(inner, 0, 0)

	inner = PutCoreModuleSection(inner, consumerCore)
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "prodl", Sort: CoreSortFunc, Idx: 1},
		{Name: "fread", Sort: CoreSortFunc, Idx: 2},
	})
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0})

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype)
	inner = PutCanonSectionLiftAsync(inner, 3, 2)
	inner = PutExportSectionOneFunc(inner, "run", 1)

	producer := buildFutureProducerComponent(producerCore, resultValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, "run")
}

func buildStreamProducerComponent(producerCore []byte, elemValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype))

	swParams, swResults := []byte{0x7f, 0x7f, 0x7f}, []byte{0x7f}
	buf = PutCoreModuleSection(buf, TrampolineModuleForParamsResults(swParams, swResults))
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "0")

	buf = PutCanonTaskReturnTypeIdx(buf, 0)
	buf = PutCanonStreamNew(buf, 0)

	buf = PutCoreModuleSection(buf, producerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 1},
		{Name: "snew", Sort: CoreSortFunc, Idx: 2},
		{Name: "sw", Sort: CoreSortFunc, Idx: 0},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem")
	buf = PutCanonStreamWrite(buf, 0, 0)

	buf = PutCoreModuleSection(buf, FixupModuleForParamsResults(swParams, swResults))
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 3},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3})

	buf = PutAliasSectionCoreExportFunc(buf, 2, "prod")
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0)
	buf = PutCanonSectionLiftAsync(buf, 4, 1)
	buf = PutExportSectionOneFunc(buf, "prod", 0)
	return buf
}

func BuildStreamExportImportComponent(producerCore, consumerCore, memCore []byte, elemValtype, resultValtype byte) []byte {
	inner := PutComponentHeader(nil)
	inner = PutTypeSectionOneDefined(inner, InnerTypeStream(elemValtype))
	inner = PutTypeSectionOneFuncResultIdxAsync(inner, nil, nil, 0)
	inner = PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{1})

	inner = PutCoreModuleSection(inner, memCore)
	inner = PutCoreInstanceSectionInstantiate(inner, 0)
	inner = PutAliasSectionCoreExport(inner, CoreSortMemory, 0, "m")

	inner = PutCanonTaskReturnSingle(inner, resultValtype)
	inner = PutCanonSectionLowerAsync(inner, 0, 0)
	inner = PutCanonStreamRead(inner, 0, 0)

	inner = PutCoreModuleSection(inner, consumerCore)
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "prodl", Sort: CoreSortFunc, Idx: 1},
		{Name: "sread", Sort: CoreSortFunc, Idx: 2},
	})
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0})

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype)
	inner = PutCanonSectionLiftAsync(inner, 3, 2)
	inner = PutExportSectionOneFunc(inner, "run", 1)

	producer := buildStreamProducerComponent(producerCore, elemValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, "run")
}
