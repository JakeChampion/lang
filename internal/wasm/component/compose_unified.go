package component

// compose_unified.go is the single entry point that subsumes the four
// shape-specific composers (CLI-stream / TCP / UDP / HTTP). A driver
// classifies a core module's preview-2 imports into a ComposeRequest;
// Compose surfaces the union of shared types via the engine's idempotent
// ensure* methods, declares every import as a data-driven gImport, and
// emits the chosen tail. Because surfacing is idempotent and the lowering
// loop is data-driven, ANY mix of imports composes — sockets + files,
// stdout + stderr together, args + env, an HTTP handler that also logs,
// etc. — which is what lets the toolchain drop the preview-1 adapter.

// ComposeRequest is the full preview-2 import surface a core module
// needs, across every shape. Each field is independent; the driver sets
// the ones the core actually imports.
type ComposeRequest struct {
	// CLI stdio (independent — a program may use stdout AND stderr).
	Stdout, Stderr, Stdin bool

	// io/streams methods + drops the core imports directly. BlockWrite /
	// BlockRead back print/eprint, stdin reads, the file write/read
	// chains, and a TCP connection's send/recv. DropInput / DropOutput
	// are Reader/Writer close() → canon resource.drop.
	BlockWrite, BlockRead bool
	DropInput, DropOutput bool

	// Filesystem open-chain (mutually exclusive single descriptor mode,
	// the combined read+write, or read+write+append for a program that
	// mixes all three open modes in one run).
	FileRead, FileWrite, FileAppend, FileReadWrite, FileReadWriteAppend bool

	// Socket / HTTP method surfaces.
	Tcp  bool // TCP server (listen/accept/close)
	Udp  bool // send-only UDP client
	Http bool // wasi:http/incoming-handler

	// Standalone CLI capabilities. WallNow is wasi:clocks/wall-clock.now
	// (a memory trampoline). Args / Env are wasi:cli/environment
	// get-arguments / get-environment (memory+realloc, shared interface).
	WallNow, Args, Env bool

	// Structured no-opt imports (exit / random / monotonic).
	Structured []WasiImport

	// Tail: when Http, exports wasi:http/incoming-handler. Otherwise
	// ExportName == "" lifts wasi:cli/run; a non-empty name lifts a u32
	// component export (the -component-wrap shape).
	ExportName string
}

// streamDirections reports whether the request needs the input / output
// halves of wasi:io/streams surfaced.
func (r ComposeRequest) streamDirections() (needIn, needOut bool) {
	needIn = r.Stdin || r.BlockRead || r.DropInput ||
		r.FileRead || r.FileReadWrite || r.FileReadWriteAppend || r.Tcp || r.Http
	needOut = r.Stdout || r.Stderr || r.BlockWrite || r.DropOutput ||
		r.FileWrite || r.FileAppend || r.FileReadWrite || r.FileReadWriteAppend || r.Tcp || r.Http
	return needIn, needOut
}

// Compose builds the component for a core module whose preview-2 imports
// are described by req. coreExportName is the lifted core entry
// (_lang_run for cli/run + named exports; the handle export for HTTP).
func Compose(coreBytes []byte, req ComposeRequest, coreExportName string) []byte {
	g := newGComposer()

	// Surface io/streams first with the full direction union so the
	// later ensure* calls (which request narrower directions) are no-ops.
	if needIn, needOut := req.streamDirections(); needIn || needOut {
		g.ensureIoStreams(needIn, needOut)
	}

	// Socket / HTTP surfaces.
	if req.Tcp {
		g.ensureTcp()
		g.ensureInstanceNetwork()
		g.ensureTcpCreate()
	}
	if req.Udp {
		g.ensureUdpCreate()
		g.ensureInstanceNetwork()
	}
	if req.Http {
		g.ensureHttpTypes()
	}

	// Filesystem open-chain.
	viaName, viaParams := composeReadViaName, composeReadViaParams
	switch {
	case req.FileAppend:
		viaName, viaParams = composeAppendViaName, composeAppendViaParams
		g.ensureFilesystem(gFsAppend)
	case req.FileWrite:
		viaName = composeWriteViaName
		g.ensureFilesystem(gFsWrite)
	case req.FileReadWrite:
		g.ensureFilesystem(gFsReadWrite)
	case req.FileReadWriteAppend:
		g.ensureFilesystem(gFsReadWriteAppend)
	case req.FileRead:
		g.ensureFilesystem(gFsRead)
	}
	hasFile := req.FileRead || req.FileWrite || req.FileAppend || req.FileReadWrite || req.FileReadWriteAppend

	// CLI stdio + standalone capabilities.
	if req.Stdout {
		g.ensureCliWrite("wasi:cli/stdout@0.2.0", "get-stdout")
	}
	if req.Stderr {
		g.ensureCliWrite("wasi:cli/stderr@0.2.0", "get-stderr")
	}
	if req.Stdin {
		g.ensureCliStdin()
	}
	if req.WallNow {
		g.importStandalone("wasi:clocks/wall-clock@0.2.0", WasiClocksWallClockInstanceTypeBody())
	}
	if req.Args || req.Env {
		g.ensureCliEnvironment(req.Args, req.Env)
	}
	for _, imp := range req.Structured {
		g.importStructured(imp)
	}

	const streams = "wasi:io/streams@0.2.0"

	// --- declare the lowerings ---

	if req.Tcp {
		const tcp = "wasi:sockets/tcp@0.2.0"
		g.add(
			gImport{iface: "wasi:sockets/tcp-create-socket@0.2.0", name: "create-tcp-socket", kind: gMem, params: composeTcpCreateParams},
			gImport{iface: tcp, name: "[method]tcp-socket.start-bind", kind: gMem, params: composeTcpStartBindParams},
			gImport{iface: tcp, name: "[method]tcp-socket.finish-bind", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.start-listen", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.finish-listen", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.accept", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.subscribe", kind: gNoOpt},
			gImport{iface: tcp, name: "[resource-drop]tcp-socket", kind: gDrop, resourceT: g.surfaced["tcp-socket"]},
			gImport{iface: "wasi:io/poll@0.2.0", name: "[method]pollable.block", kind: gNoOpt},
			gImport{iface: "wasi:io/poll@0.2.0", name: "[resource-drop]pollable", kind: gDrop, resourceT: g.surfaced["pollable"]},
		)
	}
	if req.Udp {
		const udp = "wasi:sockets/udp@0.2.0"
		g.add(
			gImport{iface: "wasi:sockets/udp-create-socket@0.2.0", name: "create-udp-socket", kind: gMem, params: udpCreateParams},
			gImport{iface: udp, name: "[method]udp-socket.start-bind", kind: gMem, params: udpBindStreamParams},
			gImport{iface: udp, name: "[method]udp-socket.finish-bind", kind: gMem, params: udpSelfRetParams},
			gImport{iface: udp, name: "[method]udp-socket.stream", kind: gMem, params: udpBindStreamParams},
			gImport{iface: udp, name: "[method]outgoing-datagram-stream.check-send", kind: gMem, params: udpSelfRetParams},
			gImport{iface: udp, name: "[method]outgoing-datagram-stream.send", kind: gMem, params: udpSendParams},
			gImport{iface: udp, name: "[method]outgoing-datagram-stream.subscribe", kind: gNoOpt},
			gImport{iface: udp, name: "[resource-drop]udp-socket", kind: gDrop, resourceT: g.surfaced["udp-socket"]},
			gImport{iface: udp, name: "[resource-drop]incoming-datagram-stream", kind: gDrop, resourceT: g.surfaced["incoming-datagram-stream"]},
			gImport{iface: udp, name: "[resource-drop]outgoing-datagram-stream", kind: gDrop, resourceT: g.surfaced["outgoing-datagram-stream"]},
		)
		// The send path blocks on the outgoing-datagram-stream's
		// pollable until a datagram is permitted; pull in poll.block /
		// drop unless the TCP block already declared them.
		if !req.Tcp {
			g.add(
				gImport{iface: "wasi:io/poll@0.2.0", name: "[method]pollable.block", kind: gNoOpt},
				gImport{iface: "wasi:io/poll@0.2.0", name: "[resource-drop]pollable", kind: gDrop, resourceT: g.surfaced["pollable"]},
			)
		}
	}
	if req.Tcp || req.Udp {
		g.add(gImport{iface: "wasi:sockets/instance-network@0.2.0", name: "instance-network", kind: gNoOpt})
	}
	if req.Http {
		const http = "wasi:http/types@0.2.0"
		g.add(
			gImport{iface: http, name: "[method]incoming-request.headers", kind: gNoOpt},
			gImport{iface: http, name: "[static]incoming-body.finish", kind: gNoOpt},
			gImport{iface: http, name: "[constructor]fields", kind: gNoOpt},
			gImport{iface: http, name: "[constructor]outgoing-response", kind: gNoOpt},
			gImport{iface: http, name: "[method]outgoing-response.set-status-code", kind: gNoOpt},
			gImport{iface: http, name: "[resource-drop]incoming-request", kind: gDrop, resourceT: g.surfaced["incoming-request"]},
			gImport{iface: http, name: "[resource-drop]fields", kind: gDrop, resourceT: g.surfaced["fields"]},
			gImport{iface: http, name: "[resource-drop]future-trailers", kind: gDrop, resourceT: g.surfaced["future-trailers"]},
			gImport{iface: http, name: "[method]incoming-request.consume", kind: gMem, params: httpSelfRetParams},
			gImport{iface: http, name: "[method]incoming-body.stream", kind: gMem, params: httpSelfRetParams},
			gImport{iface: http, name: "[method]fields.append", kind: gMem, params: repeatI32(6)},
			gImport{iface: http, name: "[method]outgoing-response.body", kind: gMem, params: httpSelfRetParams},
			gImport{iface: http, name: "[method]outgoing-body.write", kind: gMem, params: httpSelfRetParams},
			gImport{iface: http, name: "[static]response-outparam.set", kind: gMem, params: httpOutparamSetParams},
			gImport{iface: http, name: "[static]outgoing-body.finish", kind: gMemRealloc, params: repeatI32(4)},
			gImport{iface: http, name: "[method]incoming-request.method", kind: gMemRealloc, params: httpSelfRetParams},
			gImport{iface: http, name: "[method]incoming-request.path-with-query", kind: gMemRealloc, params: httpSelfRetParams},
			gImport{iface: http, name: "[method]fields.entries", kind: gMemRealloc, params: httpSelfRetParams},
		)
	}

	// Filesystem open-chain lowerings.
	if hasFile {
		g.add(
			gImport{iface: "wasi:filesystem/preopens@0.2.0", name: composeGetDirsName, kind: gMemRealloc, params: composeGetDirsParams},
			gImport{iface: "wasi:filesystem/types@0.2.0", name: composeOpenAtName, kind: gMem, params: composeOpenAtParams},
			gImport{iface: "wasi:filesystem/types@0.2.0", name: viaName, kind: gMem, params: viaParams},
		)
		if req.FileReadWrite || req.FileReadWriteAppend {
			g.add(gImport{iface: "wasi:filesystem/types@0.2.0", name: composeWriteViaName, kind: gMem, params: composeReadViaParams})
		}
		if req.FileReadWriteAppend {
			g.add(gImport{iface: "wasi:filesystem/types@0.2.0", name: composeAppendViaName, kind: gMem, params: composeAppendViaParams})
		}
	}

	// io/streams methods + drops.
	if req.BlockWrite {
		g.add(gImport{iface: streams, name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams})
	}
	if req.BlockRead {
		g.add(gImport{iface: streams, name: composeBlockReadName, kind: gMemRealloc, params: composeBlockReadParams})
	}
	if req.DropInput {
		g.add(gImport{iface: streams, name: "[resource-drop]input-stream", kind: gDrop, resourceT: g.surfaced["input-stream"]})
	}
	if req.DropOutput {
		g.add(gImport{iface: streams, name: "[resource-drop]output-stream", kind: gDrop, resourceT: g.surfaced["output-stream"]})
	}

	// CLI stdio getters (no-opts).
	if req.Stdout {
		g.add(gImport{iface: "wasi:cli/stdout@0.2.0", name: "get-stdout", kind: gNoOpt})
	}
	if req.Stderr {
		g.add(gImport{iface: "wasi:cli/stderr@0.2.0", name: "get-stderr", kind: gNoOpt})
	}
	if req.Stdin {
		g.add(gImport{iface: "wasi:cli/stdin@0.2.0", name: "get-stdin", kind: gNoOpt})
	}

	// Standalone capabilities.
	if req.WallNow {
		g.add(gImport{iface: "wasi:clocks/wall-clock@0.2.0", name: "now", kind: gMem, params: composeOneI32Params})
	}
	if req.Args {
		g.add(gImport{iface: "wasi:cli/environment@0.2.0", name: "get-arguments", kind: gMemRealloc, params: composeOneI32Params})
	}
	if req.Env {
		g.add(gImport{iface: "wasi:cli/environment@0.2.0", name: "get-environment", kind: gMemRealloc, params: composeOneI32Params})
	}
	for _, imp := range req.Structured {
		g.add(gImport{iface: imp.InterfaceName, name: imp.FuncName, kind: gNoOpt})
	}

	if req.Http {
		return g.finishHttp(coreBytes, coreExportName)
	}
	return g.finish(coreBytes, coreExportName, req.ExportName)
}
