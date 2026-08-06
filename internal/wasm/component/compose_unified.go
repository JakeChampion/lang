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

	// wasi:filesystem/types methods the core imports — the open-chain's
	// via-stream directions and the path mutators, as an independent set
	// rather than a mutually exclusive mode.
	File FsFeatures

	// Socket / HTTP method surfaces.
	Tcp  bool // TCP server (listen/accept/close)
	Udp  bool // send-only UDP client
	Http bool // wasi:http/incoming-handler

	// TcpConnect adds the outbound-client chain (start-connect /
	// finish-connect) to the tcp instance type + lowerings. Implies
	// Tcp. The wasm analog of the native tcp_connect; lets an edge
	// handler reach upstreams.
	TcpConnect bool

	// Reactor timer: wasi:clocks/monotonic-clock.subscribe-duration
	// (own<pollable>) + wasi:io/poll.pollable.block, composed
	// standalone (no sockets). The wasm reactor's timer primitive.
	Timer bool

	// Reactor multiplexer: wasi:io/poll.poll(list<pollable>) ->
	// list<u32>. Opts the imported wasi:io/poll instance into the
	// heavier shape that also declares `poll`. The wasm analog of the
	// native poll(fds) readiness multiplexer.
	Poll bool

	// Reactor pollable drop: wasi:io/poll.[resource-drop]pollable, so
	// the reactor frees a consumed timer pollable. Only added on the
	// standalone (timer / poll) path — the socket paths emit their own
	// pollable drop, so the lowering is gated to avoid a duplicate.
	PollableDrop bool

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
		r.File.Read || r.Tcp || r.Http
	needOut = r.Stdout || r.Stderr || r.BlockWrite || r.DropOutput ||
		r.File.Write || r.File.Append || r.Tcp || r.Http
	return needIn, needOut
}

// Compose builds the component for a core module whose preview-2 imports
// are described by req. coreExportName is the lifted core entry
// (_lang_run for cli/run + named exports; the handle export for HTTP).
func Compose(coreBytes []byte, req ComposeRequest, coreExportName string) []byte {
	g := newGComposer()
	// Decide the wasi:io/poll instance shape up front: req.Poll wants
	// the `poll(list<pollable>)` multiplexer in addition to block.
	// Set before any ensure* so ensureIoPoll builds the right shape
	// whichever dependency reaches it first (timer or socket).
	g.needPoll = req.Poll
	// Outbound client opts the tcp instance type into the connect
	// variant (start-connect / finish-connect). Set before ensureTcp.
	g.needConnect = req.TcpConnect

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
	if req.Timer {
		// If the program also imports monotonic-clock `now` (monotonic_ns),
		// build a combined instance exporting both it and subscribe-duration —
		// a component can import the interface only once.
		monoNow := false
		for _, imp := range req.Structured {
			if imp.InterfaceName == "wasi:clocks/monotonic-clock@0.2.0" {
				monoNow = true
			}
		}
		g.ensureMonotonicTimer(monoNow)
	}
	if req.Poll {
		// The multiplexer needs the io/poll instance surfaced even
		// when no socket / timer pulled it in (a poll-only program).
		g.ensureIoPoll()
	}
	if req.PollableDrop && !req.Tcp && !req.Udp {
		// Standalone reactor drop needs the io/poll instance + the
		// pollable resource surfaced (sockets already do this).
		g.ensureIoPoll()
	}

	// Filesystem.
	hasFile := req.File.Any()
	if hasFile {
		g.ensureFilesystem(req.File)
	}

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
		if req.TcpConnect {
			// Outbound client: the connect chain. The tcp instance type
			// is the connect variant (a superset of the server one), so
			// the server lowerings above still resolve; these add the
			// two connect methods. start-connect mirrors start-bind's
			// 15-param ip-socket-address flattening.
			g.add(
				gImport{iface: tcp, name: "[method]tcp-socket.start-connect", kind: gMem, params: composeTcpStartBindParams},
				gImport{iface: tcp, name: "[method]tcp-socket.finish-connect", kind: gMem, params: composeTcpSelfRetParams},
			)
		}
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

	// Reactor timer lowerings: subscribe-duration (→ own<pollable>)
	// and pollable.block. The pollable resource is surfaced by
	// ensureIoPoll (pulled in by ensureMonotonicTimer); its drop is
	// not emitted yet (the program exits with the pollable live — the
	// canonical ABI permits a leaked own handle at trap/exit). The
	// list multiplexer (wasi:io/poll.poll) lands in a later slice.
	if req.Timer {
		g.add(
			gImport{iface: "wasi:clocks/monotonic-clock@0.2.0", name: "subscribe-duration", kind: gNoOpt},
			gImport{iface: "wasi:io/poll@0.2.0", name: "[method]pollable.block", kind: gNoOpt},
		)
	}
	if req.Poll {
		// poll(list<pollable>) -> list<u32>: list param (ptr, len) +
		// list result via return area — memory + realloc lowering,
		// like fields.entries / get-arguments.
		g.add(gImport{iface: "wasi:io/poll@0.2.0", name: "poll", kind: gMemRealloc, params: repeatI32(3)})
	}
	if req.PollableDrop && !req.Tcp && !req.Udp {
		// Standalone reactor pollable drop. Gated off Tcp/Udp, which
		// already declare [resource-drop]pollable in their blocks.
		g.add(gImport{iface: "wasi:io/poll@0.2.0", name: "[resource-drop]pollable", kind: gDrop, resourceT: g.surfaced["pollable"]})
	}

	// Filesystem lowerings — one per method the core imports, in the
	// same order ensureFilesystem declared them.
	if hasFile {
		const fsTypes = "wasi:filesystem/types@0.2.0"
		g.add(gImport{iface: "wasi:filesystem/preopens@0.2.0", name: composeGetDirsName, kind: gMemRealloc, params: composeGetDirsParams})
		if req.File.OpenAt {
			g.add(gImport{iface: fsTypes, name: composeOpenAtName, kind: gMem, params: composeOpenAtParams})
		}
		if req.File.Read {
			g.add(gImport{iface: fsTypes, name: composeReadViaName, kind: gMem, params: composeReadViaParams})
		}
		if req.File.Write {
			g.add(gImport{iface: fsTypes, name: composeWriteViaName, kind: gMem, params: composeReadViaParams})
		}
		if req.File.Append {
			g.add(gImport{iface: fsTypes, name: composeAppendViaName, kind: gMem, params: composeAppendViaParams})
		}
		if req.File.Unlink {
			g.add(gImport{iface: fsTypes, name: composeUnlinkAtName, kind: gMem, params: composePathMutatorParams})
		}
		if req.File.Mkdir {
			g.add(gImport{iface: fsTypes, name: composeMkdirAtName, kind: gMem, params: composePathMutatorParams})
		}
		if req.File.Rmdir {
			g.add(gImport{iface: fsTypes, name: composeRmdirAtName, kind: gMem, params: composePathMutatorParams})
		}
		if req.File.Stat {
			g.add(gImport{iface: fsTypes, name: composeStatAtName, kind: gMem, params: composeStatAtParams})
		}
		if req.File.ReadDir {
			// read-directory-entry hands back an entry NAME, which the
			// host allocates into our memory — hence realloc, where
			// read-directory itself only yields a handle.
			g.add(
				gImport{iface: fsTypes, name: composeReadDirName, kind: gMem, params: composeSelfRetParams},
				gImport{iface: fsTypes, name: composeDirEntryName, kind: gMemRealloc, params: composeSelfRetParams},
				gImport{iface: fsTypes, name: composeDirStreamDrop, kind: gDrop, resourceT: g.surfaced["directory-entry-stream"]},
			)
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
