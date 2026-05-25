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
// and EXPORTS wasi:http/incoming-handler. It runs on the general
// composition engine (compose_general.go): ensureHttpTypes surfaces the
// shared resources, each imported method is declared as a gImport
// lowering by its kind (no-opts scalar / memory trampoline /
// memory+realloc trampoline) or a canon resource.drop, and
// finishHttp lifts + exports the handler via emitIncomingHandlerExport.
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
//
// extras / structured carry the standalone (no shared-resource-deps)
// CLI capabilities a handler may also use — now() / env() / args()
// (mem trampolines, MemTramp) and exit / random / monotonic (no-opts,
// Structured). Each is imported as its own instance and packaged into
// its own per-interface arg, so an HTTP handler that stamps a timestamp
// (now()) or reads config (env()) composes adapter-free. This is the
// same capability set the CLI-stream composer (ComposePreview2CliRun)
// handles; sharing it is the composer-unification work.
func ComposeHttpHandler(coreBytes []byte, hasStdout, hasStderr bool, extras []MemTrampImport, structured []WasiImport, coreExportName string) []byte {
	g := newGComposer()

	// Surface wasi:http/types (drags in io/streams read+write) + optional
	// CLI write streams + standalone capabilities.
	g.ensureHttpTypes()
	if hasStdout {
		g.ensureCliWrite("wasi:cli/stdout@0.2.0", "get-stdout")
	}
	if hasStderr {
		g.ensureCliWrite("wasi:cli/stderr@0.2.0", "get-stderr")
	}
	for _, mt := range extras {
		g.importStandalone(mt.InterfaceName, mt.InstanceTypeBody)
	}
	for _, imp := range structured {
		g.importStructured(imp)
	}

	const http = "wasi:http/types@0.2.0"
	const streams = "wasi:io/streams@0.2.0"

	// Declare the lowerings. no-opts: scalar in/out, no memory.
	g.add(
		gImport{iface: http, name: "[method]incoming-request.headers", kind: gNoOpt},
		gImport{iface: http, name: "[static]incoming-body.finish", kind: gNoOpt},
		gImport{iface: http, name: "[constructor]fields", kind: gNoOpt},
		gImport{iface: http, name: "[constructor]outgoing-response", kind: gNoOpt},
		gImport{iface: http, name: "[method]outgoing-response.set-status-code", kind: gNoOpt},
	)
	// resource.drops.
	g.add(
		gImport{iface: http, name: "[resource-drop]incoming-request", kind: gDrop, resourceT: g.surfaced["incoming-request"]},
		gImport{iface: http, name: "[resource-drop]fields", kind: gDrop, resourceT: g.surfaced["fields"]},
		gImport{iface: http, name: "[resource-drop]future-trailers", kind: gDrop, resourceT: g.surfaced["future-trailers"]},
		gImport{iface: streams, name: "[resource-drop]input-stream", kind: gDrop, resourceT: g.surfaced["input-stream"]},
		gImport{iface: streams, name: "[resource-drop]output-stream", kind: gDrop, resourceT: g.surfaced["output-stream"]},
	)
	// memory trampolines (result through retptr / params read from memory).
	g.add(
		gImport{iface: http, name: "[method]incoming-request.consume", kind: gMem, params: httpSelfRetParams},
		gImport{iface: http, name: "[method]incoming-body.stream", kind: gMem, params: httpSelfRetParams},
		gImport{iface: http, name: "[method]fields.append", kind: gMem, params: repeatI32(6)},
		gImport{iface: http, name: "[method]outgoing-response.body", kind: gMem, params: httpSelfRetParams},
		gImport{iface: http, name: "[method]outgoing-body.write", kind: gMem, params: httpSelfRetParams},
		gImport{iface: http, name: "[static]response-outparam.set", kind: gMem, params: httpOutparamSetParams},
		gImport{iface: streams, name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams},
	)
	// memory+realloc (host returns variable-length data into guest memory).
	g.add(
		gImport{iface: http, name: "[static]outgoing-body.finish", kind: gMemRealloc, params: repeatI32(4)},
		gImport{iface: http, name: "[method]incoming-request.method", kind: gMemRealloc, params: httpSelfRetParams},
		gImport{iface: http, name: "[method]incoming-request.path-with-query", kind: gMemRealloc, params: httpSelfRetParams},
		gImport{iface: http, name: "[method]fields.entries", kind: gMemRealloc, params: httpSelfRetParams},
		gImport{iface: streams, name: composeBlockReadName, kind: gMemRealloc, params: composeBlockReadParams},
	)
	if hasStdout {
		g.add(gImport{iface: "wasi:cli/stdout@0.2.0", name: "get-stdout", kind: gNoOpt})
	}
	if hasStderr {
		g.add(gImport{iface: "wasi:cli/stderr@0.2.0", name: "get-stderr", kind: gNoOpt})
	}
	for _, mt := range extras {
		kind := gMem
		if mt.NeedsRealloc {
			kind = gMemRealloc
		}
		g.add(gImport{iface: mt.InterfaceName, name: mt.FuncName, kind: kind, params: composeOneI32Params})
	}
	for _, imp := range structured {
		g.add(gImport{iface: imp.InterfaceName, name: imp.FuncName, kind: gNoOpt})
	}

	return g.finishHttp(coreBytes, coreExportName)
}

