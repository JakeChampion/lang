package component

// compose.go is the general preview-2 → cli/run composition engine.
//
// Each bespoke WrapWasi* helper hand-computed the interleaved
// component/core index spaces for one fixed import shape, which made
// every new combination its own ~100-line wrap. ComposePreview2CliRun
// replaces that family for the CLI-stream + structured world: given a
// description of which preview-2 imports the core module needs, it
// composes the component by walking a fixed canonical instance order
// and lowering each function according to its kind (no-memory simple
// lower, memory-only trampoline, or memory+realloc trampoline). A
// small stateful builder (p2composer) tracks every index space so the
// arithmetic — the fragile part — is bumped exactly once per emit.
//
// It currently subsumes: structured-only (exit/random/monotonic),
// print/eprint, read_line, read_line+print, the read_file open-chain
// (get-directories → open-at → read-via-stream → blocking-read), the
// write_file open-chain (… → write-via-stream → blocking-write), and
// any mix of those — including new combinations the bespoke wraps
// never covered (read_line+exit, read_file+print, write_file+exit,
// write_file+print, eprint+monotonic, ...). read_file and write_file
// in one program (both descriptor stream directions) plus open_* and
// TCP stay on their own wraps for now.

// ComposeOpts describes the CLI-stream + structured imports a core
// module needs. WriteGetter is "get-stdout", "get-stderr", or "" (no
// write side); ReadStdin enables the stdin read side; FileRead
// enables the read_file open-chain (get-directories → open-at →
// read-via-stream → input-stream.blocking-read); FileWrite enables
// the write_file open-chain (get-directories → open-at →
// write-via-stream → output-stream.blocking-write-and-flush);
// Structured lists the no-memory structured imports
// (exit/random/monotonic) in the order they should appear. FileRead
// and FileWrite are mutually exclusive (the filesystem/types
// instance type carries one descriptor method direction).
type ComposeOpts struct {
	WriteGetter string
	ReadStdin   bool
	FileRead    bool
	FileWrite   bool
	Structured  []WasiImport
}

// blockWriteParams / blockReadParams are the core import signatures
// the trampolines mirror: blocking-write-and-flush lowers to
// (i32,i32,i32,i32)->() and blocking-read to (i32,i64,i32)->().
var (
	composeBlockWriteParams = []byte{0x7f, 0x7f, 0x7f, 0x7f}
	composeBlockReadParams  = []byte{0x7f, 0x7e, 0x7f}
	composeGetDirsParams    = []byte{0x7f} // (ret_ptr)
	composeOpenAtParams     = []byte{0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f}
	composeReadViaParams    = []byte{0x7f, 0x7e, 0x7f} // (self, offset, ret_ptr)
)

const (
	composeBlockWriteName = "[method]output-stream.blocking-write-and-flush"
	composeBlockReadName  = "[method]input-stream.blocking-read"
	composeGetDirsName    = "get-directories"
	composeOpenAtName     = "[method]descriptor.open-at"
	composeReadViaName    = "[method]descriptor.read-via-stream"
	composeWriteViaName   = "[method]descriptor.write-via-stream"
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

func (c *p2composer) coreInstOneFunc(name string, coreFuncIdx uint32) uint32 {
	c.buf = PutCoreInstanceSectionFromOneFuncExport(c.buf, name, coreFuncIdx)
	idx := c.nCoreInst
	c.nCoreInst++
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

// ComposePreview2CliRun builds a wasi:cli/run component for a core
// module that imports any mix of CLI-stream functions (stdout/stderr
// write via print, stdin read via read_line), the read_file /
// write_file open-chains (FileRead / FileWrite), and no-memory
// structured functions (exit/random/monotonic). coreExportName is the
// lifted run entry (e.g. "_lang_run").
func ComposePreview2CliRun(coreBytes []byte, opts ComposeOpts, coreExportName string) []byte {
	hasWriteGetter := opts.WriteGetter != ""
	hasStdin := opts.ReadStdin
	hasFileRead := opts.FileRead
	hasFileWrite := opts.FileWrite
	// blocking-read on input-stream backs both stdin reads and the
	// read_file open-chain; blocking-write-and-flush on output-stream
	// backs both print/eprint and the write_file open-chain. cli/stdin
	// and cli/std{out,err} are only for the stdin / print sides.
	hasInputStream := hasStdin || hasFileRead
	hasOutputStream := hasWriteGetter || hasFileWrite
	// get-directories + open-at + (read|write)-via-stream + the
	// filesystem/types + preopens instances.
	hasFile := hasFileRead || hasFileWrite
	needStreams := hasOutputStream || hasInputStream
	// blocking-read and get-directories both return lists → realloc.
	needRealloc := hasInputStream || hasFile
	// Which descriptor stream method the file open-chain uses.
	viaName := composeReadViaName
	if hasFileWrite {
		viaName = composeWriteViaName
	}

	c := &p2composer{buf: PutComponentHeader(nil)}

	// --- Phase A: imported instances + shared-type aliases. ---
	var streamsInst uint32
	var tOut, tIn uint32
	if needStreams {
		tErr := c.typeRaw(WasiIoErrorInstanceTypeBody())
		errInst := c.importInstance("wasi:io/error@0.2.0", tErr)
		tErrAlias := c.aliasType(errInst, "error")

		var streamsBody []byte
		switch {
		case hasOutputStream && hasInputStream:
			streamsBody = WasiIoStreamsReadWriteInstanceTypeBody(tErrAlias)
		case hasOutputStream:
			streamsBody = WasiIoStreamsInstanceTypeBody(tErrAlias)
		default:
			streamsBody = WasiIoStreamsReadInstanceTypeBody(tErrAlias)
		}
		tStreams := c.typeRaw(streamsBody)
		streamsInst = c.importInstance("wasi:io/streams@0.2.0", tStreams)
		if hasOutputStream {
			tOut = c.aliasType(streamsInst, "output-stream")
		}
		if hasInputStream {
			tIn = c.aliasType(streamsInst, "input-stream")
		}
	}

	// Filesystem open path: import wasi:filesystem/types (read or
	// write body, referencing input-stream / output-stream) then
	// wasi:filesystem/preopens (referencing the descriptor that types
	// exports). Emitted right after io/streams so the stream aliases
	// (tIn / tOut) are in scope.
	var fsTypesInst, preopensInst uint32
	if hasFile {
		var fsBody []byte
		if hasFileWrite {
			fsBody = WasiFilesystemTypesWritePathInstanceTypeBody(tOut)
		} else {
			fsBody = WasiFilesystemTypesReadPathInstanceTypeBody(tIn)
		}
		tFsTypes := c.typeRaw(fsBody)
		fsTypesInst = c.importInstance("wasi:filesystem/types@0.2.0", tFsTypes)
		tDesc := c.aliasType(fsTypesInst, "descriptor")
		tPreopens := c.typeRaw(WasiFilesystemPreopensInstanceTypeBody(tDesc))
		preopensInst = c.importInstance("wasi:filesystem/preopens@0.2.0", tPreopens)
	}

	var cliWInst, cliRInst uint32
	var cliWInterface string
	if hasWriteGetter {
		var body []byte
		if opts.WriteGetter == "get-stderr" {
			body = WasiCliStderrInstanceTypeBody(tOut)
			cliWInterface = "wasi:cli/stderr@0.2.0"
		} else {
			body = WasiCliStdoutInstanceTypeBody(tOut)
			cliWInterface = "wasi:cli/stdout@0.2.0"
		}
		tCliW := c.typeRaw(body)
		cliWInst = c.importInstance(cliWInterface, tCliW)
	}
	if hasStdin {
		tCliR := c.typeRaw(WasiCliStdinInstanceTypeBody(tIn))
		cliRInst = c.importInstance("wasi:cli/stdin@0.2.0", tCliR)
	}

	structInst := make([]uint32, len(opts.Structured))
	for i, imp := range opts.Structured {
		ti := c.structuredType(imp)
		structInst[i] = c.importInstance(imp.InterfaceName, ti)
	}

	// --- Phase B: core modules (user + trampoline/fixup pairs). ---
	userMod := c.coreModule(coreBytes)
	var trampWMod, fixupWMod, trampRMod, fixupRMod uint32
	var trampDirsMod, fixupDirsMod, trampOpenMod, fixupOpenMod, trampViaMod, fixupViaMod uint32
	if hasOutputStream {
		trampWMod = c.coreModule(TrampolineModuleForParamsNoResult(composeBlockWriteParams))
		fixupWMod = c.coreModule(FixupModuleForParamsNoResult(composeBlockWriteParams))
	}
	if hasInputStream {
		trampRMod = c.coreModule(TrampolineModuleForParamsNoResult(composeBlockReadParams))
		fixupRMod = c.coreModule(FixupModuleForParamsNoResult(composeBlockReadParams))
	}
	if hasFile {
		trampDirsMod = c.coreModule(TrampolineModuleForParamsNoResult(composeGetDirsParams))
		fixupDirsMod = c.coreModule(FixupModuleForParamsNoResult(composeGetDirsParams))
		trampOpenMod = c.coreModule(TrampolineModuleForParamsNoResult(composeOpenAtParams))
		fixupOpenMod = c.coreModule(FixupModuleForParamsNoResult(composeOpenAtParams))
		// read-via-stream and write-via-stream share the (self, offset,
		// ret_ptr) shape.
		trampViaMod = c.coreModule(TrampolineModuleForParamsNoResult(composeReadViaParams))
		fixupViaMod = c.coreModule(FixupModuleForParamsNoResult(composeReadViaParams))
	}

	// --- Phase C: instantiate trampolines. ---
	var trampWInst, trampRInst uint32
	var trampDirsInst, trampOpenInst, trampViaInst uint32
	if hasOutputStream {
		trampWInst = c.instantiate(trampWMod)
	}
	if hasInputStream {
		trampRInst = c.instantiate(trampRMod)
	}
	if hasFile {
		trampDirsInst = c.instantiate(trampDirsMod)
		trampOpenInst = c.instantiate(trampOpenMod)
		trampViaInst = c.instantiate(trampViaMod)
	}

	// --- Phase D: no-memory lowers + arg-instance packaging. ---
	// User-module instantiation args, keyed by import module name.
	var argNames []string
	var argInsts []uint32

	if hasWriteGetter {
		cf := c.aliasInstFunc(cliWInst, opts.WriteGetter)
		coreF := c.lowerNoOpts(cf)
		wrap := c.coreInstOneFunc(opts.WriteGetter, coreF)
		argNames = append(argNames, cliWInterface)
		argInsts = append(argInsts, wrap)
	}
	if hasStdin {
		cf := c.aliasInstFunc(cliRInst, "get-stdin")
		coreF := c.lowerNoOpts(cf)
		wrap := c.coreInstOneFunc("get-stdin", coreF)
		argNames = append(argNames, "wasi:cli/stdin@0.2.0")
		argInsts = append(argInsts, wrap)
	}
	for i, imp := range opts.Structured {
		cf := c.aliasInstFunc(structInst[i], imp.FuncName)
		coreF := c.lowerNoOpts(cf)
		wrap := c.coreInstOneFunc(imp.FuncName, coreF)
		argNames = append(argNames, imp.CoreImportModule)
		argInsts = append(argInsts, wrap)
	}

	// Filesystem args package the open-chain trampoline placeholders:
	// preopens {get-directories} and filesystem/types
	// {open-at, read|write-via-stream} (all fixed up after user inst).
	if hasFile {
		dirsTramp := c.aliasCoreFunc(trampDirsInst, "0")
		preopensArg := c.coreInstOneFunc(composeGetDirsName, dirsTramp)
		argNames = append(argNames, "wasi:filesystem/preopens@0.2.0")
		argInsts = append(argInsts, preopensArg)

		openTramp := c.aliasCoreFunc(trampOpenInst, "0")
		viaTramp := c.aliasCoreFunc(trampViaInst, "0")
		typesArg := c.coreInstExports([]CoreInstanceExport{
			{Name: composeOpenAtName, Sort: CoreSortFunc, Idx: openTramp},
			{Name: viaName, Sort: CoreSortFunc, Idx: viaTramp},
		})
		argNames = append(argNames, "wasi:filesystem/types@0.2.0")
		argInsts = append(argInsts, typesArg)
	}

	// io/streams arg packages the trampoline placeholder funcs for
	// whichever stream methods are used (fixed up after user inst).
	if needStreams {
		var exports []CoreInstanceExport
		if hasOutputStream {
			tf := c.aliasCoreFunc(trampWInst, "0")
			exports = append(exports, CoreInstanceExport{Name: composeBlockWriteName, Sort: CoreSortFunc, Idx: tf})
		}
		if hasInputStream {
			tf := c.aliasCoreFunc(trampRInst, "0")
			exports = append(exports, CoreInstanceExport{Name: composeBlockReadName, Sort: CoreSortFunc, Idx: tf})
		}
		streamsArg := c.coreInstExports(exports)
		argNames = append(argNames, "wasi:io/streams@0.2.0")
		argInsts = append(argInsts, streamsArg)
	}

	// --- Phase E: instantiate the user module. ---
	userInst := c.instantiateArgs(userMod, argNames, argInsts)

	// --- Phase F: alias memory / realloc / trampoline tables. ---
	var reallocFunc, tableW, tableR uint32
	var tableDirs, tableOpen, tableVia uint32
	if needStreams {
		c.aliasMemory(userInst)
	}
	// blocking-read + get-directories return lists → their canon-lower
	// needs realloc.
	if needRealloc {
		reallocFunc = c.aliasReallocFunc(userInst)
	}
	if hasOutputStream {
		tableW = c.aliasTable(trampWInst)
	}
	if hasInputStream {
		tableR = c.aliasTable(trampRInst)
	}
	if hasFile {
		tableDirs = c.aliasTable(trampDirsInst)
		tableOpen = c.aliasTable(trampOpenInst)
		tableVia = c.aliasTable(trampViaInst)
	}

	// --- Phase G: memory-dependent lowers. ---
	var writeCoreF, readCoreF uint32
	var dirsCoreF, openCoreF, viaCoreF uint32
	if hasOutputStream {
		cf := c.aliasInstFunc(streamsInst, composeBlockWriteName)
		writeCoreF = c.lowerMem(cf)
	}
	if hasInputStream {
		cf := c.aliasInstFunc(streamsInst, composeBlockReadName)
		readCoreF = c.lowerMemRealloc(cf, reallocFunc)
	}
	if hasFile {
		// get-directories returns a list → realloc; open-at +
		// read/write-via-stream need memory only.
		cfDirs := c.aliasInstFunc(preopensInst, composeGetDirsName)
		dirsCoreF = c.lowerMemRealloc(cfDirs, reallocFunc)
		cfOpen := c.aliasInstFunc(fsTypesInst, composeOpenAtName)
		openCoreF = c.lowerMem(cfOpen)
		cfVia := c.aliasInstFunc(fsTypesInst, viaName)
		viaCoreF = c.lowerMem(cfVia)
	}

	// --- Phase H: fixups (install lowered funcs into tramp tables). ---
	fixup := func(mod, table, fn uint32) {
		arg := c.coreInstExports([]CoreInstanceExport{
			{Name: "$imports", Sort: CoreSortTable, Idx: table},
			{Name: "0", Sort: CoreSortFunc, Idx: fn},
		})
		c.instantiateArgs(mod, []string{""}, []uint32{arg})
	}
	if hasOutputStream {
		fixup(fixupWMod, tableW, writeCoreF)
	}
	if hasInputStream {
		fixup(fixupRMod, tableR, readCoreF)
	}
	if hasFile {
		fixup(fixupDirsMod, tableDirs, dirsCoreF)
		fixup(fixupOpenMod, tableOpen, openCoreF)
		fixup(fixupViaMod, tableVia, viaCoreF)
	}

	// --- Phase I: wasi:cli/run tail. ---
	runCoreF := c.aliasCoreFunc(userInst, coreExportName)
	resultType := c.nType
	c.buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(c.buf, resultType)
	c.nType += 2 // result type + unit-func-returning-result type
	funcType := resultType + 1
	c.buf = PutCanonSectionLiftNoOpts(c.buf, runCoreF, funcType)
	liftedCFunc := c.nCFunc
	c.nCFunc++
	c.buf = PutInstanceSectionOnePackagedFunc(c.buf, "run", liftedCFunc)
	runInst := c.nInst
	c.nInst++
	c.buf = PutExportSectionOneInstance(c.buf, "wasi:cli/run@0.2.0", runInst)
	return c.buf
}
