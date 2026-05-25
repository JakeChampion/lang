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
// wasi:http/incoming-handler@0.2.0. httpTypesInst is the imported
// wasi:http/types instance (whose incoming-request / response-outparam
// resources the handle type aliases); handleCoreFunc is the aliased
// core handle func index.
func (c *p2composer) emitIncomingHandlerExport(httpTypesInst, handleCoreFunc uint32) {
	reqT := c.aliasType(httpTypesInst, "incoming-request")
	outparamT := c.aliasType(httpTypesInst, "response-outparam")
	ownReqT := c.typeRaw([]byte{0x01, 0x69, byte(reqT)})
	ownOutT := c.typeRaw([]byte{0x01, 0x69, byte(outparamT)})
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
// handler. A real handler core also imports the http/types methods; the
// composer that lowers those (and reuses emitIncomingHandlerExport) is
// the next brick.
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

	coreInst := c.instantiate(c.coreModule(coreBytes))
	handleCoreF := c.aliasCoreFunc(coreInst, coreExportName)

	c.emitIncomingHandlerExport(httpInst, handleCoreF)
	return c.buf
}
