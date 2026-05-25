package component

// compose_udp.go composes a wasi:cli/run component for a send-only UDP
// client (udp_send). Structurally a leaner sibling of
// ComposeTcpServerCliRun: the datagram path is NOT wasi:io/streams and a
// fire-and-forget send never blocks on a pollable, so it imports only
// wasi:sockets/{network, instance-network, udp, udp-create-socket} — no
// io/error, io/streams, or io/poll.
//
// Every imported method lowers as a memory trampoline (result through a
// caller retptr, or a list param the host reads from guest memory) — none
// need realloc, since no host call returns variable-length data into
// guest memory. instance-network is a no-opts lower; the three datagram
// resources (udp-socket, incoming-/outgoing-datagram-stream) drop via
// canon resource.drop.

var (
	udpCreateParams     = []byte{0x7f, 0x7f} // (family, retptr)
	udpSelfRetParams    = []byte{0x7f, 0x7f} // (self, retptr)
	udpBindStreamParams = repeatI32(15)      // self, (option<ip-socket-address> flat = 13), retptr
	udpSendParams       = repeatI32(4)       // self, list_ptr, list_len, retptr
)

// ComposeUdpClientCliRun wraps a core module that imports the send-only
// wasi:sockets/udp surface (create → bind → stream(connect) → check-send
// → send → drop) into a wasi:cli/run component.
//
// extras / structured carry the standalone CLI capabilities a client may
// also use — now() / env() / args() (MemTramp) and exit / random /
// monotonic (no-opts, Structured) — the same set the CLI-stream composer
// handles. A UDP client runs under wasi:cli/run (`wasmtime run`), whose
// full CLI world grants all of these, so e.g. udp telemetry stamped with
// now() or addressed from env() composes adapter-free.
func ComposeUdpClientCliRun(coreBytes []byte, extras []MemTrampImport, structured []WasiImport, coreExportName string) []byte {
	c := &p2composer{buf: PutComponentHeader(nil)}

	// --- Phase A: imports + shared-type surfacing. ---
	tNet := c.typeRaw(WasiSocketsNetworkInstanceTypeBody())
	netInst := c.importInstance("wasi:sockets/network@0.2.0", tNet)
	tNetwork := c.aliasType(netInst, "network")
	tErrCode := c.aliasType(netInst, "error-code")
	tIpSock := c.aliasType(netInst, "ip-socket-address")
	tIpFam := c.aliasType(netInst, "ip-address-family")

	tUdp := c.typeRaw(WasiSocketsUdpInstanceTypeBody(tNetwork, tErrCode, tIpSock))
	udpInst := c.importInstance("wasi:sockets/udp@0.2.0", tUdp)
	tUdpSocket := c.aliasType(udpInst, "udp-socket")
	tInStream := c.aliasType(udpInst, "incoming-datagram-stream")
	tOutStream := c.aliasType(udpInst, "outgoing-datagram-stream")

	tInstNet := c.typeRaw(WasiSocketsInstanceNetworkInstanceTypeBody(tNetwork))
	instNetInst := c.importInstance("wasi:sockets/instance-network@0.2.0", tInstNet)

	tCreate := c.typeRaw(WasiSocketsUdpCreateSocketInstanceTypeBody(tIpFam, tErrCode, tUdpSocket))
	createInst := c.importInstance("wasi:sockets/udp-create-socket@0.2.0", tCreate)

	// Standalone CLI capability instances (now / env / args; exit /
	// random / monotonic) the client may also use.
	extraInst := make([]uint32, len(extras))
	for i, mt := range extras {
		extraInst[i] = c.importInstance(mt.InterfaceName, c.typeRaw(mt.InstanceTypeBody))
	}
	structInst := make([]uint32, len(structured))
	for i, imp := range structured {
		structInst[i] = c.importInstance(imp.InterfaceName, c.structuredType(imp))
	}

	// --- Phase B: core module + trampoline/fixup pair per mem method. ---
	userMod := c.coreModule(coreBytes)
	type memMethod struct {
		inst    uint32
		name    string
		params  []byte
		realloc bool
		tramp   uint32
		fixup   uint32
		trampIn uint32
		table   uint32
		coreF   uint32
	}
	mems := []memMethod{
		{inst: createInst, name: "create-udp-socket", params: udpCreateParams},
		{inst: udpInst, name: "[method]udp-socket.start-bind", params: udpBindStreamParams},
		{inst: udpInst, name: "[method]udp-socket.finish-bind", params: udpSelfRetParams},
		{inst: udpInst, name: "[method]udp-socket.stream", params: udpBindStreamParams},
		{inst: udpInst, name: "[method]outgoing-datagram-stream.check-send", params: udpSelfRetParams},
		{inst: udpInst, name: "[method]outgoing-datagram-stream.send", params: udpSendParams},
	}
	// Standalone mem-trampoline extras (now / env / args) — each a
	// (ret_ptr)->() lower over its own instance; env / args return lists
	// so they need realloc.
	for i, mt := range extras {
		mems = append(mems, memMethod{inst: extraInst[i], name: mt.FuncName, params: composeOneI32Params, realloc: mt.NeedsRealloc})
	}
	for i := range mems {
		mems[i].tramp = c.coreModule(TrampolineModuleForParamsNoResult(mems[i].params))
		mems[i].fixup = c.coreModule(FixupModuleForParamsNoResult(mems[i].params))
	}

	// --- Phase C: instantiate trampolines. ---
	for i := range mems {
		mems[i].trampIn = c.instantiate(mems[i].tramp)
	}

	// --- Phase D: no-opts lower, resource.drops, arg packaging. ---
	instNetF := c.lowerNoOpts(c.aliasInstFunc(instNetInst, "instance-network"))
	dropSocketF := c.resourceDrop(tUdpSocket)
	dropInF := c.resourceDrop(tInStream)
	dropOutF := c.resourceDrop(tOutStream)
	memTramp := make([]uint32, len(mems))
	for i := range mems {
		memTramp[i] = c.aliasCoreFunc(mems[i].trampIn, "0")
	}

	instNetArg := c.coreInstOneFunc("instance-network", instNetF)
	createArg := c.coreInstOneFunc("create-udp-socket", memTramp[0])
	udpArg := c.coreInstExports([]CoreInstanceExport{
		{Name: "[method]udp-socket.start-bind", Sort: CoreSortFunc, Idx: memTramp[1]},
		{Name: "[method]udp-socket.finish-bind", Sort: CoreSortFunc, Idx: memTramp[2]},
		{Name: "[method]udp-socket.stream", Sort: CoreSortFunc, Idx: memTramp[3]},
		{Name: "[method]outgoing-datagram-stream.check-send", Sort: CoreSortFunc, Idx: memTramp[4]},
		{Name: "[method]outgoing-datagram-stream.send", Sort: CoreSortFunc, Idx: memTramp[5]},
		{Name: "[resource-drop]udp-socket", Sort: CoreSortFunc, Idx: dropSocketF},
		{Name: "[resource-drop]incoming-datagram-stream", Sort: CoreSortFunc, Idx: dropInF},
		{Name: "[resource-drop]outgoing-datagram-stream", Sort: CoreSortFunc, Idx: dropOutF},
	})

	argNames := []string{
		"wasi:sockets/instance-network@0.2.0",
		"wasi:sockets/udp-create-socket@0.2.0",
		"wasi:sockets/udp@0.2.0",
	}
	argInsts := []uint32{instNetArg, createArg, udpArg}
	// Standalone CLI extras: each mem-tramp extra (placeholder in
	// memTramp, after the 6 socket methods) + each no-opt structured
	// import gets its own per-interface arg instance.
	needRealloc := false
	for i, mt := range extras {
		argNames = append(argNames, mt.InterfaceName)
		argInsts = append(argInsts, c.coreInstOneFunc(mt.FuncName, memTramp[6+i]))
		if mt.NeedsRealloc {
			needRealloc = true
		}
	}
	for i, imp := range structured {
		f := c.lowerNoOpts(c.aliasInstFunc(structInst[i], imp.FuncName))
		argNames = append(argNames, imp.InterfaceName)
		argInsts = append(argInsts, c.coreInstOneFunc(imp.FuncName, f))
	}

	// --- Phase E: instantiate the user module. ---
	userInst := c.instantiateArgs(userMod, argNames, argInsts)

	// --- Phase F: alias memory (+ realloc when an extra returns a list)
	// + trampoline tables. ---
	c.aliasMemory(userInst)
	var reallocF uint32
	if needRealloc {
		reallocF = c.aliasReallocFunc(userInst)
	}
	for i := range mems {
		mems[i].table = c.aliasTable(mems[i].trampIn)
	}

	// --- Phase G: memory lowers (list-returning extras need realloc). ---
	for i := range mems {
		cf := c.aliasInstFunc(mems[i].inst, mems[i].name)
		if mems[i].realloc {
			mems[i].coreF = c.lowerMemRealloc(cf, reallocF)
		} else {
			mems[i].coreF = c.lowerMem(cf)
		}
	}

	// --- Phase H: fixups (install lowered funcs into tramp tables). ---
	for i := range mems {
		arg := c.coreInstExports([]CoreInstanceExport{
			{Name: "$imports", Sort: CoreSortTable, Idx: mems[i].table},
			{Name: "0", Sort: CoreSortFunc, Idx: mems[i].coreF},
		})
		c.instantiateArgs(mems[i].fixup, []string{""}, []uint32{arg})
	}

	// --- Phase I: wasi:cli/run tail. ---
	runCoreF := c.aliasCoreFunc(userInst, coreExportName)
	resultType := c.nType
	c.buf = PutTypeSectionResultEmptyAndUnitFuncReturningResult(c.buf, resultType)
	c.nType += 2
	c.buf = PutCanonSectionLiftNoOpts(c.buf, runCoreF, resultType+1)
	liftedCFunc := c.nCFunc
	c.nCFunc++
	c.buf = PutInstanceSectionOnePackagedFunc(c.buf, "run", liftedCFunc)
	runInst := c.nInst
	c.nInst++
	c.buf = PutExportSectionOneInstance(c.buf, "wasi:cli/run@0.2.0", runInst)
	return c.buf
}
