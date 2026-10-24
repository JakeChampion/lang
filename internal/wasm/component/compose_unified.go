package component

type ComposeRequest struct {
	Stdout, Stderr, Stdin bool

	BlockWrite, BlockRead bool
	StreamReactor         bool
	UdpRecv               bool
	DropInput, DropOutput bool
	DropError             bool

	File FsFeatures

	Tcp  bool
	Udp  bool
	Http bool

	TcpConnect bool

	Timer bool

	Poll bool

	PollableDrop bool

	WallNow, Args, Env bool

	Config bool

	Structured []WasiImport

	ExportName string
}

func (r ComposeRequest) streamDirections() (needIn, needOut bool) {
	needIn = r.Stdin || r.BlockRead || r.DropInput ||
		r.File.Read || r.Tcp || r.Http || r.StreamReactor
	needOut = r.Stdout || r.Stderr || r.BlockWrite || r.DropOutput ||
		r.File.Write || r.File.Append || r.Tcp || r.Http || r.StreamReactor
	return needIn, needOut
}

func Compose(coreBytes []byte, req ComposeRequest, coreExportName string) []byte {
	g := newGComposer()
	g.needPoll = req.Poll
	g.needConnect = req.TcpConnect

	g.needStreamReactor = req.StreamReactor
	if req.StreamReactor {
		g.ensureIoPoll()
	}
	if needIn, needOut := req.streamDirections(); needIn || needOut {
		g.ensureIoStreams(needIn, needOut)
	}

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
		monoNow := false
		for _, imp := range req.Structured {
			if imp.InterfaceName == "wasi:clocks/monotonic-clock@0.2.0" {
				monoNow = true
			}
		}
		g.ensureMonotonicTimer(monoNow)
	}
	if req.Poll {
		g.ensureIoPoll()
	}
	if req.PollableDrop && !req.Tcp && !req.Udp {
		g.ensureIoPoll()
	}

	hasFile := req.File.Any()
	if hasFile {
		g.ensureFilesystem(req.File)
	}

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
	if req.Config {
		g.importStandalone(wasiConfigStore, WasiConfigStoreInstanceTypeBody())
	}
	for _, imp := range req.Structured {
		g.importStructured(imp)
	}

	const streams = "wasi:io/streams@0.2.0"

	if req.Tcp {
		const tcp = "wasi:sockets/tcp@0.2.0"
		g.add(
			gImport{iface: "wasi:sockets/tcp-create-socket@0.2.0", name: "create-tcp-socket", kind: gMem, params: composeTcpCreateParams},
			gImport{iface: tcp, name: "[method]tcp-socket.start-bind", kind: gMem, params: composeTcpStartBindParams},
			gImport{iface: tcp, name: "[method]tcp-socket.finish-bind", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.start-listen", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.finish-listen", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.accept", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.local-address", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.remote-address", kind: gMem, params: composeTcpSelfRetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.set-listen-backlog-size", kind: gMem, params: composeTcpSelfI64RetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.set-keep-alive-enabled", kind: gMem, params: composeTcpSelfI32RetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.shutdown", kind: gMem, params: composeTcpSelfI32RetParams},
			gImport{iface: tcp, name: "[method]tcp-socket.subscribe", kind: gNoOpt},
			gImport{iface: tcp, name: "[resource-drop]tcp-socket", kind: gDrop, resourceT: g.surfaced["tcp-socket"]},
		)
		if req.TcpConnect {
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
			gImport{iface: udp, name: "[method]udp-socket.local-address", kind: gMem, params: udpSelfRetParams},
			gImport{iface: udp, name: "[method]udp-socket.remote-address", kind: gMem, params: udpSelfRetParams},
			gImport{iface: udp, name: "[method]incoming-datagram-stream.subscribe", kind: gNoOpt},
			gImport{iface: udp, name: "[resource-drop]udp-socket", kind: gDrop, resourceT: g.surfaced["udp-socket"]},
			gImport{iface: udp, name: "[resource-drop]incoming-datagram-stream", kind: gDrop, resourceT: g.surfaced["incoming-datagram-stream"]},
			gImport{iface: udp, name: "[resource-drop]outgoing-datagram-stream", kind: gDrop, resourceT: g.surfaced["outgoing-datagram-stream"]},
		)
	}
	if req.UdpRecv {
		g.add(gImport{iface: "wasi:sockets/udp@0.2.0", name: "[method]incoming-datagram-stream.receive", kind: gMemRealloc, params: composeTcpSelfI64RetParams})
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

	if req.Tcp || req.Udp || req.Timer {
		g.add(gImport{iface: "wasi:io/poll@0.2.0", name: "[method]pollable.block", kind: gNoOpt})
	}
	if req.Tcp || req.Udp || req.PollableDrop {
		g.add(gImport{iface: "wasi:io/poll@0.2.0", name: "[resource-drop]pollable", kind: gDrop, resourceT: g.surfaced["pollable"]})
	}
	if req.Timer {
		g.add(gImport{iface: "wasi:clocks/monotonic-clock@0.2.0", name: "subscribe-duration", kind: gNoOpt})
	}
	if req.Poll {
		g.add(gImport{iface: "wasi:io/poll@0.2.0", name: "poll", kind: gMemRealloc, params: repeatI32(3)})
	}

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
		if req.File.Link {
			g.add(gImport{iface: fsTypes, name: composeLinkAtName, kind: gMem, params: composeLinkAtParams})
		}
		if req.File.Symlink {
			g.add(gImport{iface: fsTypes, name: composeSymlinkAtName, kind: gMem, params: composeSymlinkAtParams})
		}
		if req.File.Readlink {
			g.add(gImport{iface: fsTypes, name: composeReadlinkAtName, kind: gMemRealloc, params: composePathMutatorParams})
		}
		if req.File.Rename {
			g.add(gImport{iface: fsTypes, name: composeRenameAtName, kind: gMem, params: composeRenameAtParams})
		}
		if req.File.SetTimes {
			g.add(gImport{iface: fsTypes, name: composeSetTimesAtName, kind: gMem, params: composeSetTimesAtParams})
		}
		if req.File.SetSize {
			g.add(gImport{iface: fsTypes, name: composeSetSizeName, kind: gMem, params: composeSetSizeParams})
		}
		if req.File.Sync {
			g.add(gImport{iface: fsTypes, name: composeSyncName, kind: gMem, params: composeSelfRetParams})
		}
		if req.File.SyncData {
			g.add(gImport{iface: fsTypes, name: composeSyncDataName, kind: gMem, params: composeSelfRetParams})
		}
		if req.File.Advise {
			g.add(gImport{iface: fsTypes, name: composeAdviseName, kind: gMem, params: composeAdviseParams})
		}
		if req.File.Stat {
			g.add(gImport{iface: fsTypes, name: composeStatAtName, kind: gMem, params: composeStatAtParams})
		}
		if req.File.StatSelf {
			g.add(gImport{iface: fsTypes, name: composeStatName, kind: gMem, params: composeSelfRetParams})
		}
		if req.File.ReadDir {
			g.add(
				gImport{iface: fsTypes, name: composeReadDirName, kind: gMem, params: composeSelfRetParams},
				gImport{iface: fsTypes, name: composeDirEntryName, kind: gMemRealloc, params: composeSelfRetParams},
				gImport{iface: fsTypes, name: composeDirStreamDrop, kind: gDrop, resourceT: g.surfaced["directory-entry-stream"]},
			)
		}
		if req.File.DropDesc {
			g.add(gImport{iface: fsTypes, name: "[resource-drop]descriptor", kind: gDrop, resourceT: g.surfaced["descriptor"]})
		}
	}

	if req.BlockWrite {
		g.add(gImport{iface: streams, name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams})
	}
	if req.BlockRead {
		g.add(gImport{iface: streams, name: composeBlockReadName, kind: gMemRealloc, params: composeBlockReadParams})
	}
	if req.StreamReactor {
		g.add(gImport{iface: streams, name: "[method]input-stream.subscribe", kind: gNoOpt})
		g.add(gImport{iface: streams, name: "[method]output-stream.subscribe", kind: gNoOpt})
		g.add(gImport{iface: streams, name: "[method]input-stream.read", kind: gMemRealloc, params: composeBlockReadParams})
	}
	if req.DropInput {
		g.add(gImport{iface: streams, name: "[resource-drop]input-stream", kind: gDrop, resourceT: g.surfaced["input-stream"]})
	}
	if req.DropOutput {
		g.add(gImport{iface: streams, name: "[resource-drop]output-stream", kind: gDrop, resourceT: g.surfaced["output-stream"]})
	}
	if req.DropError {
		g.add(gImport{iface: "wasi:io/error@0.2.0", name: "[resource-drop]error", kind: gDrop, resourceT: g.ensureIoError()})
	}

	if req.Stdout {
		g.add(gImport{iface: "wasi:cli/stdout@0.2.0", name: "get-stdout", kind: gNoOpt})
	}
	if req.Stderr {
		g.add(gImport{iface: "wasi:cli/stderr@0.2.0", name: "get-stderr", kind: gNoOpt})
	}
	if req.Stdin {
		g.add(gImport{iface: "wasi:cli/stdin@0.2.0", name: "get-stdin", kind: gNoOpt})
	}

	if req.WallNow {
		g.add(gImport{iface: "wasi:clocks/wall-clock@0.2.0", name: "now", kind: gMem, params: composeOneI32Params})
	}
	if req.Args {
		g.add(gImport{iface: "wasi:cli/environment@0.2.0", name: "get-arguments", kind: gMemRealloc, params: composeOneI32Params})
	}
	if req.Env {
		g.add(gImport{iface: "wasi:cli/environment@0.2.0", name: "get-environment", kind: gMemRealloc, params: composeOneI32Params})
	}
	if req.Config {
		g.add(gImport{iface: wasiConfigStore, name: "get", kind: gMemRealloc, params: composeConfigGetParams})
	}
	for _, imp := range req.Structured {
		g.add(gImport{iface: imp.InterfaceName, name: imp.FuncName, kind: gNoOpt})
	}

	if req.Http {
		return g.finishHttp(coreBytes, coreExportName)
	}
	return g.finish(coreBytes, coreExportName, req.ExportName)
}
