package component

// compose_tcp.go composes a wasi:cli/run component for a TCP-server
// program (tcp_listen / tcp_accept / tcp_recv / tcp_send / tcp_close).
// TCP is its own self-contained shape — it imports the full
// wasi:sockets + wasi:io/poll surface and nothing from the CLI-stream
// world — so it gets a dedicated assembly rather than a dimension on
// ComposePreview2CliRun. It reuses the p2composer index tracker.
//
// The imported instances (in dependency order): io/error, io/streams
// (read+write, for the streams accept hands back), io/poll,
// sockets/network, sockets/tcp, sockets/instance-network,
// sockets/tcp-create-socket. The shared resource/value types are
// surfaced at the top level so the later instance types can
// outer-alias them.
//
// Lowering kinds:
//   - no-opts (single i32 / no result, no memory): instance-network,
//     tcp-socket.subscribe, pollable.block.
//   - resource.drop (canon built-in): tcp-socket, pollable,
//     input-stream, output-stream.
//   - memory trampolines (write result<…> through a caller retptr):
//     create-tcp-socket, tcp-socket.{start-bind, finish-bind,
//     start-listen, finish-listen, accept}. No realloc — the results
//     are fixed-size, written to the user-provided pointer.

var (
	composeTcpCreateParams    = []byte{0x7f, 0x7f} // (family, retptr)
	composeTcpSelfRetParams   = []byte{0x7f, 0x7f} // (self, retptr)
	composeTcpStartBindParams = repeatI32(15)      // self, network, disc, 11 flat, retptr
)

func repeatI32(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0x7f
	}
	return b
}

// ComposeTcpServerCliRun wraps a core module that imports the
// preview-2 TCP/sockets surface into a wasi:cli/run component.
// hasStreamRead / hasStreamWrite add the connection's
// input-stream.blocking-read / output-stream.blocking-write-and-flush
// (tcp_recv / tcp_send) — full read/write echo servers. hasEnv adds
// wasi:cli/environment.get-environment — an HTTP-over-TCP handler that
// reads its listen port from the PORT env var (the synthesised
// `main()` calls `__port_from_env`, which lowers to `env()` →
// get-environment) composes adapter-free via the extras list. hasStdout / hasStderr add
// wasi:cli/stdout.get-stdout / wasi:cli/stderr.get-stderr so a server
// that print()s / eprint()s for logging composes too (the write reuses
// the connection's output-stream.blocking-write-and-flush lowering,
// which tcpStreamUsage already detects since print imports it).
func ComposeTcpServerCliRun(coreBytes []byte, hasStreamRead, hasStreamWrite, hasStdout, hasStderr, hasStdin, hasFileRead, hasFileWrite, hasFileAppend bool, extras []MemTrampImport, structured []WasiImport, coreExportName string) []byte {
	c := &p2composer{buf: PutComponentHeader(nil)}

	// --- Phase A: imports + shared-type surfacing (dependency order). ---
	tErr := c.typeRaw(WasiIoErrorInstanceTypeBody())
	errInst := c.importInstance("wasi:io/error@0.2.0", tErr)
	tErrAlias := c.aliasType(errInst, "error")

	tStreams := c.typeRaw(WasiIoStreamsReadWriteInstanceTypeBody(tErrAlias))
	streamsInst := c.importInstance("wasi:io/streams@0.2.0", tStreams)
	tOut := c.aliasType(streamsInst, "output-stream")
	tIn := c.aliasType(streamsInst, "input-stream")

	tPoll := c.typeRaw(WasiIoPollInstanceTypeBody())
	pollInst := c.importInstance("wasi:io/poll@0.2.0", tPoll)
	tPollable := c.aliasType(pollInst, "pollable")

	tNet := c.typeRaw(WasiSocketsNetworkInstanceTypeBody())
	netInst := c.importInstance("wasi:sockets/network@0.2.0", tNet)
	_ = netInst
	tNetwork := c.aliasType(netInst, "network")
	tErrCode := c.aliasType(netInst, "error-code")
	tIpSock := c.aliasType(netInst, "ip-socket-address")
	tIpFam := c.aliasType(netInst, "ip-address-family")

	tTcp := c.typeRaw(WasiSocketsTcpInstanceTypeBody(tNetwork, tErrCode, tIpSock, tIn, tOut, tPollable))
	tcpInst := c.importInstance("wasi:sockets/tcp@0.2.0", tTcp)
	tTcpSocket := c.aliasType(tcpInst, "tcp-socket")

	tInstNet := c.typeRaw(WasiSocketsInstanceNetworkInstanceTypeBody(tNetwork))
	instNetInst := c.importInstance("wasi:sockets/instance-network@0.2.0", tInstNet)

	tCreate := c.typeRaw(WasiSocketsTcpCreateSocketInstanceTypeBody(tIpFam, tErrCode, tTcpSocket))
	createInst := c.importInstance("wasi:sockets/tcp-create-socket@0.2.0", tCreate)

	// Standalone CLI capability instances (now / env / args; exit /
	// random / monotonic) — each its own standalone instance type, no
	// shared resource deps, so they slot in after the sockets surface.
	extraInst := make([]uint32, len(extras))
	for i, mt := range extras {
		extraInst[i] = c.importInstance(mt.InterfaceName, c.typeRaw(mt.InstanceTypeBody))
	}
	structInst := make([]uint32, len(structured))
	for i, imp := range structured {
		structInst[i] = c.importInstance(imp.InterfaceName, c.structuredType(imp))
	}
	// Optional CLI write streams (print / eprint logging), over the
	// shared output-stream resource surfaced above; and stdin (get-stdin
	// returns the same input-stream resource).
	var stdoutInst, stderrInst, stdinInst uint32
	if hasStdout {
		stdoutInst = c.importInstance("wasi:cli/stdout@0.2.0", c.typeRaw(WasiCliStdoutInstanceTypeBody(tOut)))
	}
	if hasStderr {
		stderrInst = c.importInstance("wasi:cli/stderr@0.2.0", c.typeRaw(WasiCliStderrInstanceTypeBody(tOut)))
	}
	if hasStdin {
		stdinInst = c.importInstance("wasi:cli/stdin@0.2.0", c.typeRaw(WasiCliStdinInstanceTypeBody(tIn)))
	}
	// Optional filesystem open-chain (read_file → static file server;
	// write_file / open_appender → access logs / uploads). Imports
	// wasi:filesystem/types (one descriptor direction — read over the
	// input-stream, write/append over the output-stream surfaced above)
	// + wasi:filesystem/preopens. The blocking-read / blocking-write on
	// the file's stream reuses tcp_recv / tcp_send's io/streams lowering
	// (hasStreamRead/Write is set whenever blocking-read/write is
	// imported, including via the file helpers). The three directions
	// are mutually exclusive (single-direction instance type).
	hasFile := hasFileRead || hasFileWrite || hasFileAppend
	viaName, viaParams := composeReadViaName, composeReadViaParams
	switch {
	case hasFileAppend:
		viaName, viaParams = composeAppendViaName, composeAppendViaParams
	case hasFileWrite:
		viaName = composeWriteViaName
	}
	var fsTypesInst, preopensInst uint32
	if hasFile {
		var fsBody []byte
		switch {
		case hasFileAppend:
			fsBody = WasiFilesystemTypesAppendPathInstanceTypeBody(tOut)
		case hasFileWrite:
			fsBody = WasiFilesystemTypesWritePathInstanceTypeBody(tOut)
		default:
			fsBody = WasiFilesystemTypesReadPathInstanceTypeBody(tIn)
		}
		fsTypesInst = c.importInstance("wasi:filesystem/types@0.2.0", c.typeRaw(fsBody))
		tDesc := c.aliasType(fsTypesInst, "descriptor")
		preopensInst = c.importInstance("wasi:filesystem/preopens@0.2.0", c.typeRaw(WasiFilesystemPreopensInstanceTypeBody(tDesc)))
	}

	// --- Phase B: core modules (user + a trampoline/fixup pair per
	// memory-lowered method). ---
	userMod := c.coreModule(coreBytes)
	type memMethod struct {
		inst    uint32 // component instance exporting the method
		name    string // method name (alias + core export)
		params  []byte // core import signature
		tramp   uint32 // trampoline module idx
		fixup   uint32 // fixup module idx
		trampIn uint32 // trampoline core instance
		table   uint32 // aliased trampoline table
		coreF   uint32 // lowered core func
		realloc bool   // list-returning (blocking-read) → mem+realloc lower
	}
	mems := []memMethod{
		{inst: createInst, name: "create-tcp-socket", params: composeTcpCreateParams},
		{inst: tcpInst, name: "[method]tcp-socket.start-bind", params: composeTcpStartBindParams},
		{inst: tcpInst, name: "[method]tcp-socket.finish-bind", params: composeTcpSelfRetParams},
		{inst: tcpInst, name: "[method]tcp-socket.start-listen", params: composeTcpSelfRetParams},
		{inst: tcpInst, name: "[method]tcp-socket.finish-listen", params: composeTcpSelfRetParams},
		{inst: tcpInst, name: "[method]tcp-socket.accept", params: composeTcpSelfRetParams},
	}
	// tcp_send / tcp_recv operate on the accepted connection's streams.
	// Track their slice positions so the io/streams arg can reference
	// their trampoline placeholders.
	idxBlockWrite, idxBlockRead := -1, -1
	if hasStreamWrite {
		idxBlockWrite = len(mems)
		mems = append(mems, memMethod{inst: streamsInst, name: composeBlockWriteName, params: composeBlockWriteParams})
	}
	if hasStreamRead {
		idxBlockRead = len(mems)
		mems = append(mems, memMethod{inst: streamsInst, name: composeBlockReadName, params: composeBlockReadParams, realloc: true})
	}
	// File open-chain: get-directories (list result → realloc), open-at,
	// and the direction's via-stream. The file's stream uses the
	// blocking-read/write added above (hasStreamRead/Write).
	idxGetDirs, idxOpenAt, idxVia := -1, -1, -1
	if hasFile {
		idxGetDirs = len(mems)
		mems = append(mems, memMethod{inst: preopensInst, name: composeGetDirsName, params: composeGetDirsParams, realloc: true})
		idxOpenAt = len(mems)
		mems = append(mems, memMethod{inst: fsTypesInst, name: composeOpenAtName, params: composeOpenAtParams})
		idxVia = len(mems)
		mems = append(mems, memMethod{inst: fsTypesInst, name: viaName, params: viaParams})
	}
	// Standalone mem-trampoline extras (now / env / args) — each a
	// (ret_ptr)->() lower over its own instance; env / args return lists
	// so they need realloc. Track the first extra's slice position so
	// the per-extra args below can find their placeholders.
	extraMemStart := len(mems)
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

	// --- Phase D: no-opts lowers, resource.drops, arg packaging. ---
	// No-opts: instance-network () -> own<network>, subscribe (self) ->
	// own<pollable>, pollable.block (self) -> () — all single-i32 / void,
	// no memory, no trampoline.
	instNetF := c.lowerNoOpts(c.aliasInstFunc(instNetInst, "instance-network"))
	subscribeF := c.lowerNoOpts(c.aliasInstFunc(tcpInst, "[method]tcp-socket.subscribe"))
	blockF := c.lowerNoOpts(c.aliasInstFunc(pollInst, "[method]pollable.block"))
	// resource.drops.
	dropSocketF := c.resourceDrop(tTcpSocket)
	dropPollableF := c.resourceDrop(tPollable)
	dropInputF := c.resourceDrop(tIn)
	dropOutputF := c.resourceDrop(tOut)
	// Trampoline placeholders for the memory methods (fixed up later).
	memTramp := make([]uint32, len(mems))
	for i := range mems {
		memTramp[i] = c.aliasCoreFunc(mems[i].trampIn, "0")
	}

	// Per-interface arg instances.
	instNetArg := c.coreInstOneFunc("instance-network", instNetF)
	createArg := c.coreInstOneFunc("create-tcp-socket", memTramp[0])
	tcpArg := c.coreInstExports([]CoreInstanceExport{
		{Name: "[method]tcp-socket.start-bind", Sort: CoreSortFunc, Idx: memTramp[1]},
		{Name: "[method]tcp-socket.finish-bind", Sort: CoreSortFunc, Idx: memTramp[2]},
		{Name: "[method]tcp-socket.start-listen", Sort: CoreSortFunc, Idx: memTramp[3]},
		{Name: "[method]tcp-socket.finish-listen", Sort: CoreSortFunc, Idx: memTramp[4]},
		{Name: "[method]tcp-socket.accept", Sort: CoreSortFunc, Idx: memTramp[5]},
		{Name: "[method]tcp-socket.subscribe", Sort: CoreSortFunc, Idx: subscribeF},
		{Name: "[resource-drop]tcp-socket", Sort: CoreSortFunc, Idx: dropSocketF},
	})
	pollArg := c.coreInstExports([]CoreInstanceExport{
		{Name: "[method]pollable.block", Sort: CoreSortFunc, Idx: blockF},
		{Name: "[resource-drop]pollable", Sort: CoreSortFunc, Idx: dropPollableF},
	})
	streamsExports := []CoreInstanceExport{
		{Name: "[resource-drop]input-stream", Sort: CoreSortFunc, Idx: dropInputF},
		{Name: "[resource-drop]output-stream", Sort: CoreSortFunc, Idx: dropOutputF},
	}
	if idxBlockWrite >= 0 {
		streamsExports = append(streamsExports, CoreInstanceExport{Name: composeBlockWriteName, Sort: CoreSortFunc, Idx: memTramp[idxBlockWrite]})
	}
	if idxBlockRead >= 0 {
		streamsExports = append(streamsExports, CoreInstanceExport{Name: composeBlockReadName, Sort: CoreSortFunc, Idx: memTramp[idxBlockRead]})
	}
	streamsArg := c.coreInstExports(streamsExports)

	// --- Phase E: instantiate the user module. ---
	argNames := []string{
		"wasi:sockets/instance-network@0.2.0",
		"wasi:sockets/tcp-create-socket@0.2.0",
		"wasi:sockets/tcp@0.2.0",
		"wasi:io/poll@0.2.0",
		"wasi:io/streams@0.2.0",
	}
	argInsts := []uint32{instNetArg, createArg, tcpArg, pollArg, streamsArg}
	// File open-chain args: filesystem/types {open-at, via-stream} +
	// filesystem/preopens {get-directories}.
	if hasFile {
		fsArg := c.coreInstExports([]CoreInstanceExport{
			{Name: composeOpenAtName, Sort: CoreSortFunc, Idx: memTramp[idxOpenAt]},
			{Name: viaName, Sort: CoreSortFunc, Idx: memTramp[idxVia]},
		})
		preopensArg := c.coreInstOneFunc(composeGetDirsName, memTramp[idxGetDirs])
		argNames = append(argNames, "wasi:filesystem/types@0.2.0", "wasi:filesystem/preopens@0.2.0")
		argInsts = append(argInsts, fsArg, preopensArg)
	}
	// Standalone CLI extras (now / env / args) — each its own
	// per-interface arg, placeholder in memTramp after the socket +
	// stream methods.
	extraNeedsRealloc := false
	for i, mt := range extras {
		argNames = append(argNames, mt.InterfaceName)
		argInsts = append(argInsts, c.coreInstOneFunc(mt.FuncName, memTramp[extraMemStart+i]))
		if mt.NeedsRealloc {
			extraNeedsRealloc = true
		}
	}
	// CLI write-stream getters (no-opts) for print / eprint logging.
	if hasStdout {
		f := c.lowerNoOpts(c.aliasInstFunc(stdoutInst, "get-stdout"))
		argNames = append(argNames, "wasi:cli/stdout@0.2.0")
		argInsts = append(argInsts, c.coreInstOneFunc("get-stdout", f))
	}
	if hasStderr {
		f := c.lowerNoOpts(c.aliasInstFunc(stderrInst, "get-stderr"))
		argNames = append(argNames, "wasi:cli/stderr@0.2.0")
		argInsts = append(argInsts, c.coreInstOneFunc("get-stderr", f))
	}
	if hasStdin {
		f := c.lowerNoOpts(c.aliasInstFunc(stdinInst, "get-stdin"))
		argNames = append(argNames, "wasi:cli/stdin@0.2.0")
		argInsts = append(argInsts, c.coreInstOneFunc("get-stdin", f))
	}
	// No-opt structured extras (exit / random / monotonic).
	for i, imp := range structured {
		f := c.lowerNoOpts(c.aliasInstFunc(structInst[i], imp.FuncName))
		argNames = append(argNames, imp.InterfaceName)
		argInsts = append(argInsts, c.coreInstOneFunc(imp.FuncName, f))
	}
	userInst := c.instantiateArgs(userMod, argNames, argInsts)

	// --- Phase F: alias memory (+ realloc for blocking-read's list
	// result or a list-returning extra) + the trampoline tables. ---
	c.aliasMemory(userInst)
	var reallocF uint32
	if hasStreamRead || extraNeedsRealloc || hasFile {
		reallocF = c.aliasReallocFunc(userInst)
	}
	for i := range mems {
		mems[i].table = c.aliasTable(mems[i].trampIn)
	}

	// --- Phase G: memory lowers (blocking-read needs realloc). ---
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
