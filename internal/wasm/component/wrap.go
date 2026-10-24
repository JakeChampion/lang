package component

type WasiImport struct {
	InterfaceName string

	FuncName string

	ParamNames []string

	ParamValtypes []byte

	CoreImportModule string

	InnerTypes [][]byte

	ResultValtypes []byte

	RawInstanceTypeBody []byte
}

func WrapWasiImported(coreBytes []byte, imports []WasiImport) []byte {
	buf := PutComponentHeader(nil)

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
	return buf
}

func BuildAsyncLiftedExportComponent(coreBytes []byte, coreExportName, exportName string, resultValtype byte) []byte {
	buf := PutComponentHeader(nil)
	buf = PutCanonTaskReturnSingle(buf, resultValtype)
	buf = PutCoreModuleSection(buf, coreBytes)
	buf = PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)
	buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0})
	buf = PutAliasSectionCoreExportFunc(buf, 1, coreExportName)
	buf = PutTypeSectionOneFuncAsync(buf, nil, nil, resultValtype)
	buf = PutCanonSectionLiftAsync(buf, 1, 0)
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
