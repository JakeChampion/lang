package component

func buildStreamEOFProducerComponent(producerCore []byte, tramp2, fixup2 []byte, elemValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutTypeSectionOneDefined(buf, InnerTypeStream(elemValtype))

	buf = PutCoreModuleSection(buf, tramp2)
	buf = PutCoreInstanceSectionInstantiate(buf, 0)
	buf = PutAliasSectionCoreExportFunc(buf, 0, "w")
	buf = PutAliasSectionCoreExportFunc(buf, 0, "wt")

	buf = PutCanonTaskReturnTypeIdx(buf, 0)
	buf = PutCanonStreamNew(buf, 0)
	buf = PutCanonStreamDropWritable(buf, 0)
	buf = PutCanonWaitableSetNew(buf)
	buf = PutCanonWaitableJoin(buf)
	buf = PutCanonWaitableSetDrop(buf)

	buf = PutCoreModuleSection(buf, producerCore)
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 2},
		{Name: "snew", Sort: CoreSortFunc, Idx: 3},
		{Name: "sw", Sort: CoreSortFunc, Idx: 0},
		{Name: "sdw", Sort: CoreSortFunc, Idx: 4},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 5},
		{Name: "wj", Sort: CoreSortFunc, Idx: 6},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 1},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 7},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 1, []string{""}, []uint32{1})

	buf = PutAliasSectionCoreExport(buf, CoreSortMemory, 2, "mem")
	buf = PutCanonStreamWrite(buf, 0, 0)
	buf = PutCanonWaitableSetWait(buf, 0)

	buf = PutCoreModuleSection(buf, fixup2)
	buf = PutAliasSectionCoreExport(buf, CoreSortTable, 0, "$imports")
	buf = PutCoreInstanceSectionFromExports(buf, []CoreInstanceExport{
		{Name: "$imports", Sort: CoreSortTable, Idx: 0},
		{Name: "0", Sort: CoreSortFunc, Idx: 8},
		{Name: "1", Sort: CoreSortFunc, Idx: 9},
	})
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 2, []string{""}, []uint32{3})

	buf = PutAliasSectionCoreExportFunc(buf, 2, "prod")
	buf = PutTypeSectionOneFuncResultIdxAsync(buf, nil, nil, 0)
	buf = PutCanonSectionLiftAsync(buf, 10, 1)
	buf = PutExportSectionOneFunc(buf, "prod", 0)
	return buf
}

func BuildStreamCollectComponent(producerCore, consumerCore, memCore, tramp2, fixup2 []byte, elemValtype, resultValtype byte) []byte {
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
	inner = PutCanonStreamDropReadable(inner, 0)
	inner = PutCanonWaitableSetNew(inner)
	inner = PutCanonWaitableJoin(inner)
	inner = PutCanonWaitableSetWait(inner, 0)
	inner = PutCanonWaitableSetDrop(inner)

	inner = PutCoreModuleSection(inner, consumerCore)
	inner = PutCoreInstanceSectionFromExports(inner, []CoreInstanceExport{
		{Name: "tr", Sort: CoreSortFunc, Idx: 0},
		{Name: "prodl", Sort: CoreSortFunc, Idx: 1},
		{Name: "sr", Sort: CoreSortFunc, Idx: 2},
		{Name: "sdr", Sort: CoreSortFunc, Idx: 3},
		{Name: "wsn", Sort: CoreSortFunc, Idx: 4},
		{Name: "wj", Sort: CoreSortFunc, Idx: 5},
		{Name: "wsw", Sort: CoreSortFunc, Idx: 6},
		{Name: "wsd", Sort: CoreSortFunc, Idx: 7},
	})
	inner = PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0})

	inner = PutAliasSectionCoreExportFunc(inner, 2, "run")
	inner = PutTypeSectionOneFuncAsync(inner, nil, nil, resultValtype)
	inner = PutCanonSectionLiftAsync(inner, 8, 2)
	inner = PutExportSectionOneFunc(inner, "run", 1)

	producer := buildStreamEOFProducerComponent(producerCore, tramp2, fixup2, elemValtype)
	return buildAsyncImportsAwaitOuter(inner, []AsyncImportSpec{{Provider: producer, ProviderExportName: "prod"}}, "run")
}
