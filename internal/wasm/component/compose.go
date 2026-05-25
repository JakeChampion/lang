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
// write_file / open_appender open-chains (… → write/append-via-stream
// → blocking-write), and
// the standalone mem-trampoline imports (wall-clock now / args
// get-arguments / env get-environment), and any mix of those —
// including new combinations the bespoke wraps never covered
// (read_line+exit, read_file+print, write_file+print, now()+print,
// args+exit, ...) and bare open_reader/open_writer (a handle opened
// but never read/written — the open-chain with no stream method).
// read_file and write_file in one program (both descriptor stream
// directions), args+env together (shared interface), and TCP stay on
// their own wraps for now.

// ComposeOpts describes the CLI-stream + structured imports a core
// module needs. WriteGetter is "get-stdout", "get-stderr", or "" (no
// write side); ReadStdin enables the stdin read side; FileRead /
// FileWrite enable the read_file / write_file open-chains
// (get-directories → open-at → read/write-via-stream). ReadStream /
// WriteStream record whether the user module actually imports
// input-stream.blocking-read / output-stream.blocking-write-and-flush
// — these are decoupled from the producers above so a bare
// open_reader / open_writer (opens a handle but never reads/writes)
// composes without the stream method. Structured lists the no-memory
// structured imports (exit/random/monotonic) in order. FileRead and
// FileWrite are mutually exclusive (the filesystem/types instance
// type carries one descriptor method direction).
type ComposeOpts struct {
	WriteGetter string
	ReadStdin   bool
	FileRead    bool
	FileWrite   bool
	FileAppend  bool
	// FileReadWrite is read AND write of files in one program (the
	// combined-direction filesystem/types instance type). Distinct from
	// the single-direction FileRead / FileWrite / FileAppend.
	FileReadWrite bool
	ReadStream    bool
	WriteStream bool
	// DropInputStream / DropOutputStream record that the core imports
	// wasi:io/streams.[resource-drop]{input,output}-stream — a file /
	// stdin Reader.close() / Writer.close() drops its own<…stream>
	// handle via canon resource.drop. Lowered into the io/streams arg.
	DropInputStream  bool
	DropOutputStream bool
	Structured       []WasiImport
	MemTramp    []MemTrampImport
	// ExportName selects the tail: "" lifts the run func as
	// wasi:cli/run@0.2.0 (the default cli shape); a non-empty name
	// lifts it as a plain u32-returning component func exported under
	// that name (the non-cli `-component-wrap` shape).
	ExportName string
}

// MemTrampImport is a self-contained single-function import whose
// canon-lower needs memory (and optionally realloc) — the
// wall-clock now / args get-arguments / env get-environment shape.
// Unlike Structured (no-memory) imports it needs a 1-i32 trampoline
// + fixup; unlike the stream / file imports it has no shared
// resource-type dependencies, so its instance type stands alone.
type MemTrampImport struct {
	InstanceTypeBody []byte // standalone instance type (one func export)
	InterfaceName    string // import name + user-module arg name
	FuncName         string // aliased func + core-instance export name
	NeedsRealloc     bool   // list-returning funcs (args/env) need realloc
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
	composeAppendViaParams  = []byte{0x7f, 0x7f}       // (self, ret_ptr) — append, no offset
	composeOneI32Params     = []byte{0x7f}             // (ret_ptr) — wall-clock/args/env
)

const (
	composeBlockWriteName = "[method]output-stream.blocking-write-and-flush"
	composeBlockReadName  = "[method]input-stream.blocking-read"
	composeGetDirsName    = "get-directories"
	composeOpenAtName     = "[method]descriptor.open-at"
	composeReadViaName    = "[method]descriptor.read-via-stream"
	composeWriteViaName   = "[method]descriptor.write-via-stream"
	composeAppendViaName  = "[method]descriptor.append-via-stream"
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
	hasFileAppend := opts.FileAppend
	// FileReadWrite is read AND write of files in one program — the
	// combined-direction filesystem/types instance type (open-at +
	// read-via-stream AND write-via-stream). It's a distinct mode from
	// the single-direction FileRead / FileWrite / FileAppend.
	hasFileReadWrite := opts.FileReadWrite
	// write_file and open_appender share the write-side open-chain
	// (output-stream); they differ only in the via-stream method.
	writeSideFile := hasFileWrite || hasFileAppend
	hasFile := hasFileRead || writeSideFile || hasFileReadWrite
	// useBlock{Read,Write} = the user module actually imports the
	// stream method. need{Input,Output}Stream = io/streams must
	// declare + alias the resource because *some* producer
	// (get-stdin / read-via-stream result, cli/std* / write-via-stream
	// result) or the method itself references it. Decoupling these
	// lets a bare open_reader / open_writer (handle opened, never
	// read/written) compose without the blocking method.
	useBlockRead := opts.ReadStream
	useBlockWrite := opts.WriteStream
	needInputStream := hasStdin || hasFileRead || useBlockRead || opts.DropInputStream || hasFileReadWrite
	needOutputStream := hasWriteGetter || writeSideFile || useBlockWrite || opts.DropOutputStream || hasFileReadWrite
	needStreams := needInputStream || needOutputStream
	// Which descriptor stream method the file open-chain uses, and its
	// core-import signature (append-via-stream takes no offset).
	viaName := composeReadViaName
	viaParams := composeReadViaParams
	switch {
	case hasFileAppend:
		viaName = composeAppendViaName
		viaParams = composeAppendViaParams
	case hasFileWrite:
		viaName = composeWriteViaName
	}

	g := newGComposer()

	// Surface the shared types (dependency-ordered, idempotent). io/streams
	// is requested with the full direction union up front so the later
	// filesystem / cli-write / stdin surfacing finds it already fixed.
	if needStreams {
		g.ensureIoStreams(needInputStream, needOutputStream)
	}
	if hasFile {
		switch {
		case hasFileReadWrite:
			g.ensureFilesystem(gFsReadWrite)
		case hasFileAppend:
			g.ensureFilesystem(gFsAppend)
		case hasFileWrite:
			g.ensureFilesystem(gFsWrite)
		default:
			g.ensureFilesystem(gFsRead)
		}
	}
	var cliWInterface string
	if hasWriteGetter {
		if opts.WriteGetter == "get-stderr" {
			cliWInterface = "wasi:cli/stderr@0.2.0"
		} else {
			cliWInterface = "wasi:cli/stdout@0.2.0"
		}
		g.ensureCliWrite(cliWInterface, opts.WriteGetter)
	}
	if hasStdin {
		g.ensureCliStdin()
	}
	for _, imp := range opts.Structured {
		g.importStructured(imp)
	}
	for _, mt := range opts.MemTramp {
		g.importStandalone(mt.InterfaceName, mt.InstanceTypeBody)
	}

	// Declare the lowerings.
	if hasWriteGetter {
		g.add(gImport{iface: cliWInterface, name: opts.WriteGetter, kind: gNoOpt})
	}
	if hasStdin {
		g.add(gImport{iface: "wasi:cli/stdin@0.2.0", name: "get-stdin", kind: gNoOpt})
	}
	for _, imp := range opts.Structured {
		g.add(gImport{iface: imp.InterfaceName, name: imp.FuncName, kind: gNoOpt})
	}
	for _, mt := range opts.MemTramp {
		kind := gMem
		if mt.NeedsRealloc {
			kind = gMemRealloc
		}
		g.add(gImport{iface: mt.InterfaceName, name: mt.FuncName, kind: kind, params: composeOneI32Params})
	}
	if hasFile {
		g.add(
			gImport{iface: "wasi:filesystem/preopens@0.2.0", name: composeGetDirsName, kind: gMemRealloc, params: composeGetDirsParams},
			gImport{iface: "wasi:filesystem/types@0.2.0", name: composeOpenAtName, kind: gMem, params: composeOpenAtParams},
			gImport{iface: "wasi:filesystem/types@0.2.0", name: viaName, kind: gMem, params: viaParams},
		)
		if hasFileReadWrite {
			// write-via-stream shares read-via's (self, offset, ret_ptr).
			g.add(gImport{iface: "wasi:filesystem/types@0.2.0", name: composeWriteViaName, kind: gMem, params: composeReadViaParams})
		}
	}
	if useBlockWrite {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams})
	}
	if useBlockRead {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: composeBlockReadName, kind: gMemRealloc, params: composeBlockReadParams})
	}
	if opts.DropInputStream {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: "[resource-drop]input-stream", kind: gDrop, resourceT: g.surfaced["input-stream"]})
	}
	if opts.DropOutputStream {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: "[resource-drop]output-stream", kind: gDrop, resourceT: g.surfaced["output-stream"]})
	}

	return g.finish(coreBytes, coreExportName, opts.ExportName)
}
