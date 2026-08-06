package component

// compose.go holds the shared preview-2 composition infrastructure: the
// p2composer index tracker (which bumps every component/core index space
// exactly once per emit — the fragile arithmetic) and the core-import
// signature constants the trampolines mirror. The unified composer
// (compose_unified.go) and the general engine (compose_general.go) build
// on this; WasiImport describes an individual structured import.

// blockWriteParams / blockReadParams are the core import signatures
// the trampolines mirror: blocking-write-and-flush lowers to
// (i32,i32,i32,i32)->() and blocking-read to (i32,i64,i32)->().
var (
	composeBlockWriteParams = []byte{0x7f, 0x7f, 0x7f, 0x7f}
	composeBlockReadParams  = []byte{0x7f, 0x7e, 0x7f}
	composeGetDirsParams    = []byte{0x7f} // (ret_ptr)
	composeOpenAtParams     = []byte{0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f}
	composeReadViaParams    = []byte{0x7f, 0x7e, 0x7f} // (self, offset, ret_ptr)
	composeAppendViaParams  = []byte{0x7f, 0x7f}       // (self, ret_ptr) — append, no offset
	composeOneI32Params     = []byte{0x7f}             // (ret_ptr) — wall-clock/args/env

	// The path mutators all lower identically: the string param flattens
	// to (ptr, len) and the `result<_, error-code>` return goes through a
	// return area, so (self, path_ptr, path_len, ret_ptr) -> ().
	composePathMutatorParams = []byte{0x7f, 0x7f, 0x7f, 0x7f}

	// stat-at takes a path-flags argument the mutators do not:
	// (self, path-flags, path_ptr, path_len, ret_ptr) -> ().
	composeStatAtParams = []byte{0x7f, 0x7f, 0x7f, 0x7f, 0x7f}

	// read-directory and read-directory-entry both take just a handle
	// and a return area: (self, ret_ptr) -> ().
	composeSelfRetParams = []byte{0x7f, 0x7f}
)

const (
	composeBlockWriteName = "[method]output-stream.blocking-write-and-flush"
	composeBlockReadName  = "[method]input-stream.blocking-read"
	composeGetDirsName    = "get-directories"
	composeOpenAtName     = "[method]descriptor.open-at"
	composeReadViaName    = "[method]descriptor.read-via-stream"
	composeWriteViaName   = "[method]descriptor.write-via-stream"
	composeAppendViaName  = "[method]descriptor.append-via-stream"
	composeUnlinkAtName   = "[method]descriptor.unlink-file-at"
	composeMkdirAtName    = "[method]descriptor.create-directory-at"
	composeStatAtName     = "[method]descriptor.stat-at"
	composeRmdirAtName    = "[method]descriptor.remove-directory-at"
	composeReadDirName    = "[method]descriptor.read-directory"
	composeDirEntryName   = "[method]directory-entry-stream.read-directory-entry"
	composeDirStreamDrop  = "[resource-drop]directory-entry-stream"
)

type p2composer struct {
	buf       []byte
	nType     uint32 // next component type index
	nInst     uint32 // next component instance index
	nCFunc    uint32 // next component func index
	nMod      uint32 // next core module index
	nCoreFunc uint32 // next core func index
	nCoreInst uint32 // next core instance index
	nCoreTab  uint32 // next aliased core table index
}

func (c *p2composer) typeRaw(body []byte) uint32 {
	c.buf = PutTypeSectionRawBody(c.buf, body)
	idx := c.nType
	c.nType++
	return idx
}

func (c *p2composer) importInstance(name string, typeidx uint32) uint32 {
	c.buf = PutImportSectionOneInstance(c.buf, name, typeidx)
	idx := c.nInst
	c.nInst++
	return idx
}

func (c *p2composer) aliasType(instIdx uint32, name string) uint32 {
	c.buf = PutAliasSectionInstanceExportType(c.buf, instIdx, name)
	idx := c.nType
	c.nType++
	return idx
}

func (c *p2composer) coreModule(mod []byte) uint32 {
	c.buf = PutCoreModuleSection(c.buf, mod)
	idx := c.nMod
	c.nMod++
	return idx
}

func (c *p2composer) instantiate(modIdx uint32) uint32 {
	c.buf = PutCoreInstanceSectionInstantiate(c.buf, modIdx)
	idx := c.nCoreInst
	c.nCoreInst++
	return idx
}

func (c *p2composer) instantiateArgs(modIdx uint32, names []string, insts []uint32) uint32 {
	c.buf = PutCoreInstanceSectionInstantiateWithInstanceArgs(c.buf, modIdx, names, insts)
	idx := c.nCoreInst
	c.nCoreInst++
	return idx
}

func (c *p2composer) aliasInstFunc(instIdx uint32, name string) uint32 {
	c.buf = PutAliasSectionInstanceExportFunc(c.buf, instIdx, name)
	idx := c.nCFunc
	c.nCFunc++
	return idx
}

func (c *p2composer) lowerNoOpts(cfuncIdx uint32) uint32 {
	c.buf = PutCanonSectionLowerNoOpts(c.buf, cfuncIdx)
	idx := c.nCoreFunc
	c.nCoreFunc++
	return idx
}

func (c *p2composer) lowerMem(cfuncIdx uint32) uint32 {
	c.buf = PutCanonSectionLowerWithMemory(c.buf, cfuncIdx, 0)
	idx := c.nCoreFunc
	c.nCoreFunc++
	return idx
}

func (c *p2composer) lowerMemRealloc(cfuncIdx, realloc uint32) uint32 {
	c.buf = PutCanonSectionLowerWithMemoryRealloc(c.buf, cfuncIdx, 0, realloc)
	idx := c.nCoreFunc
	c.nCoreFunc++
	return idx
}

func (c *p2composer) coreInstExports(exports []CoreInstanceExport) uint32 {
	c.buf = PutCoreInstanceSectionFromExports(c.buf, exports)
	idx := c.nCoreInst
	c.nCoreInst++
	return idx
}

func (c *p2composer) aliasCoreFunc(coreInstIdx uint32, name string) uint32 {
	c.buf = PutAliasSectionCoreExportFunc(c.buf, coreInstIdx, name)
	idx := c.nCoreFunc
	c.nCoreFunc++
	return idx
}

func (c *p2composer) resourceDrop(resourceTypeidx uint32) uint32 {
	c.buf = PutCanonResourceDrop(c.buf, resourceTypeidx)
	idx := c.nCoreFunc
	c.nCoreFunc++
	return idx
}

func (c *p2composer) aliasMemory(coreInstIdx uint32) {
	c.buf = PutAliasSectionCoreExport(c.buf, CoreSortMemory, coreInstIdx, "memory")
}

func (c *p2composer) aliasReallocFunc(coreInstIdx uint32) uint32 {
	c.buf = PutAliasSectionCoreExport(c.buf, CoreSortFunc, coreInstIdx, "cabi_realloc")
	idx := c.nCoreFunc
	c.nCoreFunc++
	return idx
}

func (c *p2composer) aliasTable(coreInstIdx uint32) uint32 {
	c.buf = PutAliasSectionCoreExport(c.buf, CoreSortTable, coreInstIdx, "$imports")
	idx := c.nCoreTab
	c.nCoreTab++
	return idx
}

func (c *p2composer) structuredType(imp WasiImport) uint32 {
	if imp.RawInstanceTypeBody != nil {
		c.buf = PutTypeSectionRawBody(c.buf, imp.RawInstanceTypeBody)
	} else {
		c.buf = PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(c.buf, imp.InnerTypes, imp.FuncName, imp.ParamNames, imp.ParamValtypes, imp.ResultValtypes)
	}
	idx := c.nType
	c.nType++
	return idx
}
