package component

type gLowerKind int

const (
	gNoOpt gLowerKind = iota
	gMem
	gMemRealloc
	gDrop
)

type gImport struct {
	iface     string
	name      string
	kind      gLowerKind
	params    []byte
	results   []byte
	resourceT uint32

	trampMod, fixupMod, trampInst, table, coreF uint32
	placeholder                                 uint32
}

type gComposer struct {
	c                 *p2composer
	surfaced          map[string]uint32
	inst              map[string]uint32
	imports           []gImport
	needPoll          bool
	needStreamReactor bool
	needConnect       bool
}

func newGComposer() *gComposer {
	return &gComposer{
		c:        &p2composer{buf: PutComponentHeader(nil)},
		surfaced: map[string]uint32{},
		inst:     map[string]uint32{},
	}
}

func (g *gComposer) add(imports ...gImport) { g.imports = append(g.imports, imports...) }

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

func (g *gComposer) ensureIoStreams(needIn, needOut bool) {
	if _, ok := g.inst["wasi:io/streams@0.2.0"]; ok {
		return
	}
	errAlias := g.ensureIoError()
	var body []byte
	switch {
	case g.needStreamReactor:
		body = WasiIoStreamsReadWriteReactorInstanceTypeBody(errAlias, g.surfaced["pollable"])
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
	g.ensureIoPoll()
	inst := g.c.importInstance("wasi:sockets/udp@0.2.0",
		g.c.typeRaw(WasiSocketsUdpInstanceTypeBody(g.surfaced["network"], g.surfaced["error-code"], g.surfaced["ip-socket-address"], g.surfaced["pollable"])))
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
	inst := g.c.importInstance("wasi:io/poll@0.2.0", g.c.typeRaw(WasiIoPollInstanceTypeBody(g.needPoll)))
	g.inst["wasi:io/poll@0.2.0"] = inst
	g.surfaced["pollable"] = g.c.aliasType(inst, "pollable")
}

func (g *gComposer) ensureTcp() {
	if _, ok := g.inst["wasi:sockets/tcp@0.2.0"]; ok {
		return
	}
	g.ensureNetwork()
	g.ensureIoStreams(true, true)
	g.ensureIoPoll()
	tcpBody := WasiSocketsTcpInstanceTypeBody
	if g.needConnect {
		tcpBody = WasiSocketsTcpConnectInstanceTypeBody
	}
	inst := g.c.importInstance("wasi:sockets/tcp@0.2.0",
		g.c.typeRaw(tcpBody(g.surfaced["network"], g.surfaced["error-code"], g.surfaced["ip-socket-address"], g.surfaced["input-stream"], g.surfaced["output-stream"], g.surfaced["pollable"])))
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

func (g *gComposer) ensureMonotonicTimer(withNow bool) {
	if _, ok := g.inst["wasi:clocks/monotonic-clock@0.2.0"]; ok {
		return
	}
	g.ensureIoPoll()
	var typeBody []byte
	if withNow {
		typeBody = WasiClocksMonotonicTimerAndNowInstanceTypeBody(g.surfaced["pollable"])
	} else {
		typeBody = WasiClocksMonotonicTimerInstanceTypeBody(g.surfaced["pollable"])
	}
	inst := g.c.importInstance("wasi:clocks/monotonic-clock@0.2.0", g.c.typeRaw(typeBody))
	g.inst["wasi:clocks/monotonic-clock@0.2.0"] = inst
}

func (g *gComposer) ensureCliStdin() {
	if _, ok := g.inst["wasi:cli/stdin@0.2.0"]; ok {
		return
	}
	g.ensureIoStreams(true, false)
	g.inst["wasi:cli/stdin@0.2.0"] = g.c.importInstance("wasi:cli/stdin@0.2.0", g.c.typeRaw(WasiCliStdinInstanceTypeBody(g.surfaced["input-stream"])))
}

func (g *gComposer) ensureFilesystem(f FsFeatures) {
	if _, ok := g.inst["wasi:filesystem/types@0.2.0"]; ok {
		return
	}
	needIn, needOut := f.Read, f.Write || f.Append
	if needIn || needOut {
		g.ensureIoStreams(needIn, needOut)
	}
	body := WasiFilesystemTypesPathInstanceTypeBody(g.surfaced["input-stream"], g.surfaced["output-stream"], f)
	inst := g.c.importInstance("wasi:filesystem/types@0.2.0", g.c.typeRaw(body))
	g.inst["wasi:filesystem/types@0.2.0"] = inst
	tDesc := g.c.aliasType(inst, "descriptor")
	g.surfaced["descriptor"] = tDesc
	if f.ReadDir {
		g.surfaced["directory-entry-stream"] = g.c.aliasType(inst, "directory-entry-stream")
	}
	g.inst["wasi:filesystem/preopens@0.2.0"] = g.c.importInstance("wasi:filesystem/preopens@0.2.0", g.c.typeRaw(WasiFilesystemPreopensInstanceTypeBody(tDesc)))
}

func (g *gComposer) ensureHttpTypes() {
	if _, ok := g.inst["wasi:http/types@0.2.0"]; ok {
		return
	}
	g.ensureIoStreams(true, true)
	inst := g.c.importInstance("wasi:http/types@0.2.0",
		g.c.typeRaw(WasiHttpTypesInstanceTypeBody(g.surfaced["input-stream"], g.surfaced["output-stream"])))
	g.inst["wasi:http/types@0.2.0"] = inst
	g.surfaced["incoming-request"] = g.c.aliasType(inst, "incoming-request")
	g.surfaced["fields"] = g.c.aliasType(inst, "fields")
	g.surfaced["future-trailers"] = g.c.aliasType(inst, "future-trailers")
	g.surfaced["response-outparam"] = g.c.aliasType(inst, "response-outparam")
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

func (g *gComposer) ensureCliEnvironment(args, env bool) {
	if _, ok := g.inst["wasi:cli/environment@0.2.0"]; ok {
		return
	}
	var body []byte
	switch {
	case args && env:
		body = WasiCliEnvironmentArgsAndEnvInstanceTypeBody()
	case env:
		body = WasiCliEnvironmentGetEnvironmentInstanceTypeBody()
	default:
		body = WasiCliEnvironmentArgsInstanceTypeBody()
	}
	g.inst["wasi:cli/environment@0.2.0"] = g.c.importInstance("wasi:cli/environment@0.2.0", g.c.typeRaw(body))
}

func (g *gComposer) importStandalone(iface string, body []byte) {
	if _, ok := g.inst[iface]; ok {
		return
	}
	g.inst[iface] = g.c.importInstance(iface, g.c.typeRaw(body))
}

func (g *gComposer) importStructured(imp WasiImport) {
	if _, ok := g.inst[imp.InterfaceName]; ok {
		return
	}
	g.inst[imp.InterfaceName] = g.c.importInstance(imp.InterfaceName, g.c.structuredType(imp))
}

func (g *gComposer) lower(coreBytes []byte) uint32 {
	c := g.c
	userMod := c.coreModule(coreBytes)
	for i := range g.imports {
		if g.imports[i].kind == gMem || g.imports[i].kind == gMemRealloc {
			g.imports[i].trampMod = c.coreModule(TrampolineModuleForParamsResults(g.imports[i].params, g.imports[i].results))
			g.imports[i].fixupMod = c.coreModule(FixupModuleForParamsResults(g.imports[i].params, g.imports[i].results))
		}
	}
	for i := range g.imports {
		if g.imports[i].kind == gMem || g.imports[i].kind == gMemRealloc {
			g.imports[i].trampInst = c.instantiate(g.imports[i].trampMod)
		}
	}
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
	userInst := c.instantiateArgs(userMod, argNames, argInsts)
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
	for i := range g.imports {
		im := &g.imports[i]
		switch im.kind {
		case gMem:
			im.coreF = c.lowerMem(c.aliasInstFunc(g.inst[im.iface], im.name))
		case gMemRealloc:
			im.coreF = c.lowerMemRealloc(c.aliasInstFunc(g.inst[im.iface], im.name), reallocF)
		}
	}
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
	return userInst
}

func (g *gComposer) finish(coreBytes []byte, coreExportName, tailExportName string) []byte {
	c := g.c
	userInst := g.lower(coreBytes)
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

func (g *gComposer) finishHttp(coreBytes []byte, coreExportName string) []byte {
	c := g.c
	userInst := g.lower(coreBytes)
	handleCoreF := c.aliasCoreFunc(userInst, coreExportName)
	c.emitIncomingHandlerExport(g.surfaced["incoming-request"], g.surfaced["response-outparam"], handleCoreF)
	return c.buf
}
