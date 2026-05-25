package component

// compose_general.go is the unified preview-2 composition engine that
// subsumes the shape-specific composers (CLI-stream / TCP / UDP / HTTP).
//
// The shape-specific composers each hand-assembled Phase A (import +
// shared-type surfacing) and the Phase B–H lowering dance for one fixed
// import shape, so every new cross-shape combination (TCP+UDP, UDP+files,
// …) needed its own bespoke wiring. This engine replaces that: a caller
//
//   1. surfaces the shared types it needs via idempotent, dependency-
//      ordered ensure* methods (ensureTcp pulls in network + io/streams +
//      io/poll automatically; calling ensureNetwork again is a no-op);
//   2. declares each import the core uses as a gImport lowering entry
//      (no-opt / mem / mem+realloc / canon resource.drop), tagged with
//      the wasi:X interface the user module imports it from;
//   3. calls finish(coreBytes, tail) to run the generic phase machinery
//      and emit the chosen tail (cli/run, a named export, or
//      wasi:http/incoming-handler).
//
// Because surfacing is idempotent and the lowering loop is data-driven,
// ANY mix of imports composes — the basis for retiring the adapter.
//
// Migration is incremental: composers move onto the engine one at a time
// (behaviour-preserving, gated by their e2e tests). UDP is first.

type gLowerKind int

const (
	gNoOpt      gLowerKind = iota // scalar in/out, no memory (canon lower no-opts)
	gMem                          // memory trampoline, no realloc
	gMemRealloc                   // memory trampoline + realloc (list/string results)
	gDrop                         // canon resource.drop
)

// gImport is one preview-2 import the core needs lowered. iface is the
// wasi:X instance the user module imports it from; name is the
// method/func/[resource-drop] name. For gDrop, resourceT is the surfaced
// resource type index; params is unused. For gMem/gMemRealloc, params is
// the core import signature the trampoline mirrors.
type gImport struct {
	iface     string
	name      string
	kind      gLowerKind
	params    []byte
	resourceT uint32

	// filled during finish():
	trampMod, fixupMod, trampInst, table, coreF uint32
	placeholder                                 uint32 // mem: the trampoline core func the arg references
}

// gComposer wraps the index-tracking p2composer with idempotent
// shared-type surfacing.
type gComposer struct {
	c        *p2composer
	surfaced map[string]uint32 // shared type name → component type index
	inst     map[string]uint32 // wasi:X interface → imported instance index
	imports  []gImport
}

func newGComposer() *gComposer {
	return &gComposer{
		c:        &p2composer{buf: PutComponentHeader(nil)},
		surfaced: map[string]uint32{},
		inst:     map[string]uint32{},
	}
}

// add records an import to lower. Returns the receiver for chaining.
func (g *gComposer) add(imports ...gImport) { g.imports = append(g.imports, imports...) }

// --- idempotent shared-type surfacing (dependency-ordered) ---

func (g *gComposer) ensureIoError() uint32 {
	if t, ok := g.surfaced["error"]; ok {
		return t
	}
	inst := g.c.importInstance("wasi:io/error@0.2.0", g.c.typeRaw(WasiIoErrorInstanceTypeBody()))
	g.inst["wasi:io/error@0.2.0"] = inst
	t := g.c.aliasType(inst, "error")
	g.surfaced["error"] = t
	return t
}

// ensureIoStreams imports wasi:io/streams with the directions needed and
// surfaces input-stream / output-stream. Idempotent: the first call fixes
// the instance type's directions, so callers must request the union up
// front (the engine's callers do — they know the full import set).
func (g *gComposer) ensureIoStreams(needIn, needOut bool) {
	if _, ok := g.inst["wasi:io/streams@0.2.0"]; ok {
		return
	}
	errAlias := g.ensureIoError()
	var body []byte
	switch {
	case needIn && needOut:
		body = WasiIoStreamsReadWriteInstanceTypeBody(errAlias)
	case needOut:
		body = WasiIoStreamsInstanceTypeBody(errAlias)
	default:
		body = WasiIoStreamsReadInstanceTypeBody(errAlias)
	}
	inst := g.c.importInstance("wasi:io/streams@0.2.0", g.c.typeRaw(body))
	g.inst["wasi:io/streams@0.2.0"] = inst
	if needOut {
		g.surfaced["output-stream"] = g.c.aliasType(inst, "output-stream")
	}
	if needIn {
		g.surfaced["input-stream"] = g.c.aliasType(inst, "input-stream")
	}
}

func (g *gComposer) ensureNetwork() {
	if _, ok := g.inst["wasi:sockets/network@0.2.0"]; ok {
		return
	}
	inst := g.c.importInstance("wasi:sockets/network@0.2.0", g.c.typeRaw(WasiSocketsNetworkInstanceTypeBody()))
	g.inst["wasi:sockets/network@0.2.0"] = inst
	g.surfaced["network"] = g.c.aliasType(inst, "network")
	g.surfaced["error-code"] = g.c.aliasType(inst, "error-code")
	g.surfaced["ip-socket-address"] = g.c.aliasType(inst, "ip-socket-address")
	g.surfaced["ip-address-family"] = g.c.aliasType(inst, "ip-address-family")
}

func (g *gComposer) ensureInstanceNetwork() {
	if _, ok := g.inst["wasi:sockets/instance-network@0.2.0"]; ok {
		return
	}
	g.ensureNetwork()
	inst := g.c.importInstance("wasi:sockets/instance-network@0.2.0",
		g.c.typeRaw(WasiSocketsInstanceNetworkInstanceTypeBody(g.surfaced["network"])))
	g.inst["wasi:sockets/instance-network@0.2.0"] = inst
}

func (g *gComposer) ensureUdp() {
	if _, ok := g.inst["wasi:sockets/udp@0.2.0"]; ok {
		return
	}
	g.ensureNetwork()
	inst := g.c.importInstance("wasi:sockets/udp@0.2.0",
		g.c.typeRaw(WasiSocketsUdpInstanceTypeBody(g.surfaced["network"], g.surfaced["error-code"], g.surfaced["ip-socket-address"])))
	g.inst["wasi:sockets/udp@0.2.0"] = inst
	g.surfaced["udp-socket"] = g.c.aliasType(inst, "udp-socket")
	g.surfaced["incoming-datagram-stream"] = g.c.aliasType(inst, "incoming-datagram-stream")
	g.surfaced["outgoing-datagram-stream"] = g.c.aliasType(inst, "outgoing-datagram-stream")
}

func (g *gComposer) ensureUdpCreate() {
	if _, ok := g.inst["wasi:sockets/udp-create-socket@0.2.0"]; ok {
		return
	}
	g.ensureUdp()
	inst := g.c.importInstance("wasi:sockets/udp-create-socket@0.2.0",
		g.c.typeRaw(WasiSocketsUdpCreateSocketInstanceTypeBody(g.surfaced["ip-address-family"], g.surfaced["error-code"], g.surfaced["udp-socket"])))
	g.inst["wasi:sockets/udp-create-socket@0.2.0"] = inst
}

func (g *gComposer) ensureIoPoll() {
	if _, ok := g.inst["wasi:io/poll@0.2.0"]; ok {
		return
	}
	inst := g.c.importInstance("wasi:io/poll@0.2.0", g.c.typeRaw(WasiIoPollInstanceTypeBody()))
	g.inst["wasi:io/poll@0.2.0"] = inst
	g.surfaced["pollable"] = g.c.aliasType(inst, "pollable")
}

// ensureTcp imports wasi:sockets/tcp and surfaces tcp-socket. It pulls in
// the full dependency chain: network (for ip-socket-address / error-code),
// io/streams read+write (accept hands back both directions), and io/poll
// (the socket's subscribe → pollable).
func (g *gComposer) ensureTcp() {
	if _, ok := g.inst["wasi:sockets/tcp@0.2.0"]; ok {
		return
	}
	g.ensureNetwork()
	g.ensureIoStreams(true, true)
	g.ensureIoPoll()
	inst := g.c.importInstance("wasi:sockets/tcp@0.2.0",
		g.c.typeRaw(WasiSocketsTcpInstanceTypeBody(g.surfaced["network"], g.surfaced["error-code"], g.surfaced["ip-socket-address"], g.surfaced["input-stream"], g.surfaced["output-stream"], g.surfaced["pollable"])))
	g.inst["wasi:sockets/tcp@0.2.0"] = inst
	g.surfaced["tcp-socket"] = g.c.aliasType(inst, "tcp-socket")
}

func (g *gComposer) ensureTcpCreate() {
	if _, ok := g.inst["wasi:sockets/tcp-create-socket@0.2.0"]; ok {
		return
	}
	g.ensureTcp()
	inst := g.c.importInstance("wasi:sockets/tcp-create-socket@0.2.0",
		g.c.typeRaw(WasiSocketsTcpCreateSocketInstanceTypeBody(g.surfaced["ip-address-family"], g.surfaced["error-code"], g.surfaced["tcp-socket"])))
	g.inst["wasi:sockets/tcp-create-socket@0.2.0"] = inst
}

func (g *gComposer) ensureCliStdin() {
	if _, ok := g.inst["wasi:cli/stdin@0.2.0"]; ok {
		return
	}
	g.ensureIoStreams(true, false)
	g.inst["wasi:cli/stdin@0.2.0"] = g.c.importInstance("wasi:cli/stdin@0.2.0", g.c.typeRaw(WasiCliStdinInstanceTypeBody(g.surfaced["input-stream"])))
}

// gFsMode selects the single-direction filesystem open-chain: read over the
// input-stream (static file server), write/append over the output-stream
// (access logs / uploads).
type gFsMode int

const (
	gFsRead gFsMode = iota
	gFsWrite
	gFsAppend
	gFsReadWrite // read AND write of files in one program (combined descriptor)
)

// ensureFilesystem imports wasi:filesystem/types (one descriptor direction)
// + wasi:filesystem/preopens and surfaces descriptor.
func (g *gComposer) ensureFilesystem(mode gFsMode) {
	if _, ok := g.inst["wasi:filesystem/types@0.2.0"]; ok {
		return
	}
	var body []byte
	switch mode {
	case gFsReadWrite:
		g.ensureIoStreams(true, true)
		body = WasiFilesystemTypesReadWritePathInstanceTypeBody(g.surfaced["input-stream"], g.surfaced["output-stream"])
	case gFsAppend:
		g.ensureIoStreams(false, true)
		body = WasiFilesystemTypesAppendPathInstanceTypeBody(g.surfaced["output-stream"])
	case gFsWrite:
		g.ensureIoStreams(false, true)
		body = WasiFilesystemTypesWritePathInstanceTypeBody(g.surfaced["output-stream"])
	default:
		g.ensureIoStreams(true, false)
		body = WasiFilesystemTypesReadPathInstanceTypeBody(g.surfaced["input-stream"])
	}
	inst := g.c.importInstance("wasi:filesystem/types@0.2.0", g.c.typeRaw(body))
	g.inst["wasi:filesystem/types@0.2.0"] = inst
	tDesc := g.c.aliasType(inst, "descriptor")
	g.surfaced["descriptor"] = tDesc
	g.inst["wasi:filesystem/preopens@0.2.0"] = g.c.importInstance("wasi:filesystem/preopens@0.2.0", g.c.typeRaw(WasiFilesystemPreopensInstanceTypeBody(tDesc)))
}

func (g *gComposer) ensureCliWrite(iface, getter string) {
	if _, ok := g.inst[iface]; ok {
		return
	}
	g.ensureIoStreams(false, true)
	var body []byte
	if getter == "get-stdout" {
		body = WasiCliStdoutInstanceTypeBody(g.surfaced["output-stream"])
	} else {
		body = WasiCliStderrInstanceTypeBody(g.surfaced["output-stream"])
	}
	g.inst[iface] = g.c.importInstance(iface, g.c.typeRaw(body))
}

// importStandalone imports a self-contained instance (its own type body,
// no shared deps) — the MemTramp (now/env/args) + Structured
// (exit/random/monotonic) capabilities.
func (g *gComposer) importStandalone(iface string, body []byte) {
	if _, ok := g.inst[iface]; ok {
		return
	}
	g.inst[iface] = g.c.importInstance(iface, g.c.typeRaw(body))
}

// importStructured imports a no-opt structured instance (exit / random /
// monotonic) — its instance type comes from the WasiImport descriptor.
func (g *gComposer) importStructured(imp WasiImport) {
	if _, ok := g.inst[imp.InterfaceName]; ok {
		return
	}
	g.inst[imp.InterfaceName] = g.c.importInstance(imp.InterfaceName, g.c.structuredType(imp))
}

// finish runs the generic Phase B–H lowering over the declared imports
// and emits the tail. tailExportName: "" → wasi:cli/run; otherwise a
// named u32 export. (incoming-handler is a later migration.)
func (g *gComposer) finish(coreBytes []byte, coreExportName, tailExportName string) []byte {
	c := g.c
	// Phase B: user module + a trampoline/fixup pair per mem import.
	userMod := c.coreModule(coreBytes)
	for i := range g.imports {
		if g.imports[i].kind == gMem || g.imports[i].kind == gMemRealloc {
			g.imports[i].trampMod = c.coreModule(TrampolineModuleForParamsNoResult(g.imports[i].params))
			g.imports[i].fixupMod = c.coreModule(FixupModuleForParamsNoResult(g.imports[i].params))
		}
	}
	// Phase C: instantiate trampolines.
	for i := range g.imports {
		if g.imports[i].kind == gMem || g.imports[i].kind == gMemRealloc {
			g.imports[i].trampInst = c.instantiate(g.imports[i].trampMod)
		}
	}
	// Phase D: no-opt lowers + resource.drops + mem placeholders; group
	// every lowered func by its arg interface.
	type ifaceArg struct {
		exports []CoreInstanceExport
	}
	order := []string{}
	args := map[string]*ifaceArg{}
	addExport := func(iface, name string, idx uint32) {
		a, ok := args[iface]
		if !ok {
			a = &ifaceArg{}
			args[iface] = a
			order = append(order, iface)
		}
		a.exports = append(a.exports, CoreInstanceExport{Name: name, Sort: CoreSortFunc, Idx: idx})
	}
	for i := range g.imports {
		im := &g.imports[i]
		switch im.kind {
		case gNoOpt:
			f := c.lowerNoOpts(c.aliasInstFunc(g.inst[im.iface], im.name))
			addExport(im.iface, im.name, f)
		case gDrop:
			f := c.resourceDrop(im.resourceT)
			addExport(im.iface, im.name, f)
		case gMem, gMemRealloc:
			im.placeholder = c.aliasCoreFunc(im.trampInst, "0")
			addExport(im.iface, im.name, im.placeholder)
		}
	}
	argNames := make([]string, len(order))
	argInsts := make([]uint32, len(order))
	for i, iface := range order {
		argNames[i] = iface
		argInsts[i] = c.coreInstExports(args[iface].exports)
	}
	// Phase E: instantiate the user module.
	userInst := c.instantiateArgs(userMod, argNames, argInsts)
	// Phase F: memory + realloc + trampoline tables. A program that only
	// uses no-opt / resource.drop imports (e.g. exit() alone) has no
	// trampoline, so it needs neither the memory alias nor realloc.
	needMem, needRealloc := false, false
	for i := range g.imports {
		switch g.imports[i].kind {
		case gMem:
			needMem = true
		case gMemRealloc:
			needMem, needRealloc = true, true
		}
	}
	if needMem {
		c.aliasMemory(userInst)
	}
	var reallocF uint32
	if needRealloc {
		reallocF = c.aliasReallocFunc(userInst)
	}
	for i := range g.imports {
		if g.imports[i].kind == gMem || g.imports[i].kind == gMemRealloc {
			g.imports[i].table = c.aliasTable(g.imports[i].trampInst)
		}
	}
	// Phase G: memory lowers.
	for i := range g.imports {
		im := &g.imports[i]
		switch im.kind {
		case gMem:
			im.coreF = c.lowerMem(c.aliasInstFunc(g.inst[im.iface], im.name))
		case gMemRealloc:
			im.coreF = c.lowerMemRealloc(c.aliasInstFunc(g.inst[im.iface], im.name), reallocF)
		}
	}
	// Phase H: fixups.
	for i := range g.imports {
		im := &g.imports[i]
		if im.kind == gMem || im.kind == gMemRealloc {
			arg := c.coreInstExports([]CoreInstanceExport{
				{Name: "$imports", Sort: CoreSortTable, Idx: im.table},
				{Name: "0", Sort: CoreSortFunc, Idx: im.coreF},
			})
			c.instantiateArgs(im.fixupMod, []string{""}, []uint32{arg})
		}
	}
	// Phase I: tail.
	runCoreF := c.aliasCoreFunc(userInst, coreExportName)
	if tailExportName != "" {
		funcType := c.nType
		c.buf = PutTypeSectionOneFunc(c.buf, nil, nil, CValtypeU32)
		c.nType++
		c.buf = PutCanonSectionLiftNoOpts(c.buf, runCoreF, funcType)
		lifted := c.nCFunc
		c.nCFunc++
		c.buf = PutExportSectionOneFunc(c.buf, tailExportName, lifted)
		return c.buf
	}
	resultType := c.nType
	c.buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(c.buf, resultType)
	c.nType += 2
	c.buf = PutCanonSectionLiftNoOpts(c.buf, runCoreF, resultType+1)
	lifted := c.nCFunc
	c.nCFunc++
	c.buf = PutInstanceSectionOnePackagedFunc(c.buf, "run", lifted)
	runInst := c.nInst
	c.nInst++
	c.buf = PutExportSectionOneInstance(c.buf, "wasi:cli/run@0.2.0", runInst)
	return c.buf
}
