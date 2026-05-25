package component

import "github.com/jakechampion/lang/internal/wasm/leb128"

// compose_http.go composes the wasi:http/incoming-handler component
// shape — the first export-of-interface in the codebase. Unlike
// wasi:cli/run (export an instance whose `run` func takes no params),
// the handler EXPORTS wasi:http/incoming-handler, whose `handle` func
// takes two owned resource handles — own<incoming-request> and
// own<response-outparam> — that live in the IMPORTED wasi:http/types.
// So the exported func's type outer-aliases those imported resources.
//
// This brick wires the export tail against an import-free core (a stub
// that receives the two i32 handles); the full ComposeHttpHandler (real
// core importing the http/types methods, with all the method lowerings)
// is a later brick that reuses emitIncomingHandlerExport.

// handleFunctypeBody returns a top-level type-section body (vec(1)) for
// `handle: func(request: own<incoming-request>, response-out:
// own<response-outparam>)` — no result. ownReqIdx / ownOutParamIdx are
// the component type indices of the two own<…> wrappers.
func handleFunctypeBody(ownReqIdx, ownOutParamIdx uint32) []byte {
	body := []byte{0x01, 0x40, 0x02} // vec(1) types, functype, 2 params
	body = putName(body, "request")
	body = leb128.UlebU64(body, uint64(ownReqIdx))
	body = putName(body, "response-out")
	body = leb128.UlebU64(body, uint64(ownOutParamIdx))
	body = append(body, 0x01, 0x00) // resultlist: named, vec(0) — no result
	return body
}

// emitIncomingHandlerExport lifts the core `handle` func (core sig
// (i32,i32)->(), the two owned-resource handles) into a component func
// typed handle(own<incoming-request>, own<response-outparam>), packages
// it into an instance exporting "handle", and exports that instance as
// wasi:http/incoming-handler@0.2.0. reqResourceT / outparamResourceT are
// the (already-surfaced) component type indices of the incoming-request
// / response-outparam resources from the imported wasi:http/types;
// handleCoreFunc is the aliased core handle func index.
func (c *p2composer) emitIncomingHandlerExport(reqResourceT, outparamResourceT, handleCoreFunc uint32) {
	ownReqT := c.typeRaw([]byte{0x01, 0x69, byte(reqResourceT)})
	ownOutT := c.typeRaw([]byte{0x01, 0x69, byte(outparamResourceT)})
	handleFuncT := c.typeRaw(handleFunctypeBody(ownReqT, ownOutT))

	c.buf = PutCanonSectionLiftNoOpts(c.buf, handleCoreFunc, handleFuncT)
	liftedCFunc := c.nCFunc
	c.nCFunc++
	c.buf = PutInstanceSectionOnePackagedFunc(c.buf, "handle", liftedCFunc)
	hInst := c.nInst
	c.nInst++
	c.buf = PutExportSectionOneInstance(c.buf, "wasi:http/incoming-handler@0.2.0", hInst)
}

// BuildHttpIncomingHandlerComponent composes a minimal
// wasi:http/incoming-handler component from an import-free core module
// that exports `handle` (core sig (i32,i32)->()). It imports the
// dependency chain the handle type needs — wasi:io/error, wasi:io/streams
// (for the input-stream / output-stream the body methods reference in
// the http/types type), and wasi:http/types — then lifts + exports the
// handler. A real handler core also imports the http/types methods;
// ComposeHttpHandler lowers those.
func BuildHttpIncomingHandlerComponent(coreBytes []byte, coreExportName string) []byte {
	c := &p2composer{buf: PutComponentHeader(nil)}

	tErr := c.typeRaw(WasiIoErrorInstanceTypeBody())
	errInst := c.importInstance("wasi:io/error@0.2.0", tErr)
	tErrAlias := c.aliasType(errInst, "error")

	tStreams := c.typeRaw(WasiIoStreamsReadWriteInstanceTypeBody(tErrAlias))
	streamsInst := c.importInstance("wasi:io/streams@0.2.0", tStreams)
	tOut := c.aliasType(streamsInst, "output-stream")
	tIn := c.aliasType(streamsInst, "input-stream")

	tHttp := c.typeRaw(WasiHttpTypesInstanceTypeBody(tIn, tOut))
	httpInst := c.importInstance("wasi:http/types@0.2.0", tHttp)
	tReq := c.aliasType(httpInst, "incoming-request")
	tOutparam := c.aliasType(httpInst, "response-outparam")

	coreInst := c.instantiate(c.coreModule(coreBytes))
	handleCoreF := c.aliasCoreFunc(coreInst, coreExportName)

	c.emitIncomingHandlerExport(tReq, tOutparam, handleCoreF)
	return c.buf
}

// httpSelfRetParams is the core import signature of the http/types
// methods that take (self, ret_ptr) and write a result through the
// caller's retptr: incoming-request.{method,path-with-query,consume},
// incoming-body.stream, outgoing-response.body, outgoing-body.write,
// fields.entries.
var httpSelfRetParams = []byte{0x7f, 0x7f}

// httpOutparamSetParams is response-outparam.set's flattened
// result<outgoing-response, error-code> + the outparam handle — 9 core
// params, slot 4 an i64 (the error-code arm carries option<u64>, which
// the canonical-ABI variant-join widens the slot to).
var httpOutparamSetParams = []byte{0x7f, 0x7f, 0x7f, 0x7f, 0x7e, 0x7f, 0x7f, 0x7f, 0x7f}

// ComposeHttpHandler wraps a wasi:http/incoming-handler core module —
// one that imports the wasi:http/types method surface (+ io/streams for
// the request/response bodies) and exports `handle` — into an
// adapter-free component that imports wasi:http/types + wasi:io/streams
// and EXPORTS wasi:http/incoming-handler. Structurally it mirrors
// ComposeTcpServerCliRun: lower each imported method by its kind
// (no-opts scalar / memory trampoline / memory+realloc trampoline),
// drop the resources via canon resource.drop, package them into the
// per-interface arg instances, instantiate the core, then reuse
// emitIncomingHandlerExport for the lift + export tail.
//
// Lowering kinds:
//   - no-opts (scalar in/out, no memory): incoming-request.headers,
//     incoming-body.finish, [constructor]fields, [constructor]
//     outgoing-response, outgoing-response.set-status-code.
//   - memory trampoline (result through retptr / params read from
//     memory, no host-allocated data): incoming-request.consume,
//     incoming-body.stream, fields.append, outgoing-response.body,
//     outgoing-body.write, outgoing-body.finish, response-outparam.set,
//     output-stream.blocking-write-and-flush.
//   - memory+realloc (host returns variable-length data into guest
//     memory): incoming-request.method (variant method/other(string)),
//     incoming-request.path-with-query (option<string>), fields.entries
//     (list<tuple<string,list<u8>>>), input-stream.blocking-read
//     (result<list<u8>>).
//   - canon resource.drop: incoming-request, fields, future-trailers,
//     input-stream, output-stream.
//
// hasStdout / hasStderr add wasi:cli/stdout.get-stdout /
// wasi:cli/stderr.get-stderr (no-opts, () -> own<output-stream>) so a
// handler that also print()s / eprint()s for request logging composes
// adapter-free — the print's output-stream.blocking-write-and-flush is
// the same lowering the response body already uses.
func ComposeHttpHandler(coreBytes []byte, hasStdout, hasStderr bool, coreExportName string) []byte {
	c := &p2composer{buf: PutComponentHeader(nil)}

	// --- Phase A: imports + shared-type surfacing. ---
	tErr := c.typeRaw(WasiIoErrorInstanceTypeBody())
	errInst := c.importInstance("wasi:io/error@0.2.0", tErr)
	tErrAlias := c.aliasType(errInst, "error")

	tStreams := c.typeRaw(WasiIoStreamsReadWriteInstanceTypeBody(tErrAlias))
	streamsInst := c.importInstance("wasi:io/streams@0.2.0", tStreams)
	tOut := c.aliasType(streamsInst, "output-stream")
	tIn := c.aliasType(streamsInst, "input-stream")

	tHttp := c.typeRaw(WasiHttpTypesInstanceTypeBody(tIn, tOut))
	httpInst := c.importInstance("wasi:http/types@0.2.0", tHttp)
	tReq := c.aliasType(httpInst, "incoming-request")
	tFields := c.aliasType(httpInst, "fields")
	tFutTrail := c.aliasType(httpInst, "future-trailers")
	tOutparam := c.aliasType(httpInst, "response-outparam")

	// Optional CLI write streams (print / eprint logging). Each is a
	// get-{stdout,stderr}() -> own<output-stream> over the shared
	// output-stream resource.
	var stdoutInst, stderrInst uint32
	if hasStdout {
		stdoutInst = c.importInstance("wasi:cli/stdout@0.2.0", c.typeRaw(WasiCliStdoutInstanceTypeBody(tOut)))
	}
	if hasStderr {
		stderrInst = c.importInstance("wasi:cli/stderr@0.2.0", c.typeRaw(WasiCliStderrInstanceTypeBody(tOut)))
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
		{inst: httpInst, name: "[method]incoming-request.consume", params: httpSelfRetParams},
		{inst: httpInst, name: "[method]incoming-body.stream", params: httpSelfRetParams},
		{inst: httpInst, name: "[method]fields.append", params: repeatI32(6)},
		{inst: httpInst, name: "[method]outgoing-response.body", params: httpSelfRetParams},
		{inst: httpInst, name: "[method]outgoing-body.write", params: httpSelfRetParams},
		{inst: httpInst, name: "[static]response-outparam.set", params: httpOutparamSetParams},
		// outgoing-body.finish returns result<_, error-code>; the
		// error-code arm carries option<string> payloads the host may
		// write into guest memory, so its lower needs realloc.
		{inst: httpInst, name: "[static]outgoing-body.finish", params: repeatI32(4), realloc: true},
		{inst: httpInst, name: "[method]incoming-request.method", params: httpSelfRetParams, realloc: true},
		{inst: httpInst, name: "[method]incoming-request.path-with-query", params: httpSelfRetParams, realloc: true},
		{inst: httpInst, name: "[method]fields.entries", params: httpSelfRetParams, realloc: true},
		{inst: streamsInst, name: composeBlockWriteName, params: composeBlockWriteParams},
		{inst: streamsInst, name: composeBlockReadName, params: composeBlockReadParams, realloc: true},
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
	coreFuncs := map[string]uint32{}
	noOpts := []string{
		"[method]incoming-request.headers",
		"[static]incoming-body.finish",
		"[constructor]fields",
		"[constructor]outgoing-response",
		"[method]outgoing-response.set-status-code",
	}
	for _, name := range noOpts {
		coreFuncs[name] = c.lowerNoOpts(c.aliasInstFunc(httpInst, name))
	}
	coreFuncs["[resource-drop]incoming-request"] = c.resourceDrop(tReq)
	coreFuncs["[resource-drop]fields"] = c.resourceDrop(tFields)
	coreFuncs["[resource-drop]future-trailers"] = c.resourceDrop(tFutTrail)
	coreFuncs["[resource-drop]input-stream"] = c.resourceDrop(tIn)
	coreFuncs["[resource-drop]output-stream"] = c.resourceDrop(tOut)
	// Trampoline placeholders for the memory methods (fixed up later).
	for i := range mems {
		coreFuncs[mems[i].name] = c.aliasCoreFunc(mems[i].trampIn, "0")
	}

	httpExports := []CoreInstanceExport{}
	for _, name := range []string{
		"[method]incoming-request.method",
		"[method]incoming-request.path-with-query",
		"[method]incoming-request.headers",
		"[method]incoming-request.consume",
		"[resource-drop]incoming-request",
		"[method]incoming-body.stream",
		"[static]incoming-body.finish",
		"[resource-drop]future-trailers",
		"[constructor]fields",
		"[method]fields.entries",
		"[method]fields.append",
		"[resource-drop]fields",
		"[constructor]outgoing-response",
		"[method]outgoing-response.set-status-code",
		"[method]outgoing-response.body",
		"[method]outgoing-body.write",
		"[static]outgoing-body.finish",
		"[static]response-outparam.set",
	} {
		httpExports = append(httpExports, CoreInstanceExport{Name: name, Sort: CoreSortFunc, Idx: coreFuncs[name]})
	}
	httpArg := c.coreInstExports(httpExports)
	streamsArg := c.coreInstExports([]CoreInstanceExport{
		{Name: composeBlockReadName, Sort: CoreSortFunc, Idx: coreFuncs[composeBlockReadName]},
		{Name: composeBlockWriteName, Sort: CoreSortFunc, Idx: coreFuncs[composeBlockWriteName]},
		{Name: "[resource-drop]input-stream", Sort: CoreSortFunc, Idx: coreFuncs["[resource-drop]input-stream"]},
		{Name: "[resource-drop]output-stream", Sort: CoreSortFunc, Idx: coreFuncs["[resource-drop]output-stream"]},
	})
	// CLI write-stream getters (no-opts) + their per-interface arg
	// instances.
	argNames := []string{"wasi:http/types@0.2.0", "wasi:io/streams@0.2.0"}
	argInsts := []uint32{httpArg, streamsArg}
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

	// --- Phase E: instantiate the user module. ---
	userInst := c.instantiateArgs(userMod, argNames, argInsts)

	// --- Phase F: alias memory + realloc + trampoline tables. ---
	c.aliasMemory(userInst)
	reallocF := c.aliasReallocFunc(userInst)
	for i := range mems {
		mems[i].table = c.aliasTable(mems[i].trampIn)
	}

	// --- Phase G: memory lowers (realloc methods get the user realloc). ---
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

	// --- Phase I: lift core handle + export wasi:http/incoming-handler. ---
	handleCoreF := c.aliasCoreFunc(userInst, coreExportName)
	c.emitIncomingHandlerExport(tReq, tOutparam, handleCoreF)
	return c.buf
}

