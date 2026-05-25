package component

// compose_tcp.go composes a wasi:cli/run component for a TCP-server
// program (tcp_listen / tcp_accept / tcp_recv / tcp_send / tcp_close),
// on the general composition engine (compose_general.go).
//
// The shared surface is pulled in by the engine's idempotent ensure*
// methods: ensureTcp drags in network + io/streams (read+write, for the
// streams accept hands back) + io/poll; ensureInstanceNetwork and
// ensureTcpCreate ride the same network surface; optional filesystem /
// CLI-write / stdin / extras layer on top. Each import is then declared
// as a gImport lowering:
//   - no-opts (single i32 / no result, no memory): instance-network,
//     tcp-socket.subscribe, pollable.block, the CLI getters.
//   - resource.drop (canon built-in): tcp-socket, pollable,
//     input-stream, output-stream.
//   - memory trampolines (write result<…> through a caller retptr):
//     create-tcp-socket, tcp-socket.{start-bind, finish-bind,
//     start-listen, finish-listen, accept}, plus the file open-chain
//     and (with realloc) blocking-read / get-directories / list extras.

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
	g := newGComposer()

	// Surface the shared types: TCP pulls in network + io/streams
	// (read+write) + io/poll; instance-network + tcp-create-socket ride
	// the same network surface.
	g.ensureTcp()
	g.ensureInstanceNetwork()
	g.ensureTcpCreate()

	hasFile := hasFileRead || hasFileWrite || hasFileAppend
	viaName, viaParams := composeReadViaName, composeReadViaParams
	switch {
	case hasFileAppend:
		viaName, viaParams = composeAppendViaName, composeAppendViaParams
	case hasFileWrite:
		viaName = composeWriteViaName
	}
	if hasFile {
		switch {
		case hasFileAppend:
			g.ensureFilesystem(gFsAppend)
		case hasFileWrite:
			g.ensureFilesystem(gFsWrite)
		default:
			g.ensureFilesystem(gFsRead)
		}
	}
	if hasStdout {
		g.ensureCliWrite("wasi:cli/stdout@0.2.0", "get-stdout")
	}
	if hasStderr {
		g.ensureCliWrite("wasi:cli/stderr@0.2.0", "get-stderr")
	}
	if hasStdin {
		g.ensureCliStdin()
	}
	for _, mt := range extras {
		g.importStandalone(mt.InterfaceName, mt.InstanceTypeBody)
	}
	for _, imp := range structured {
		g.importStructured(imp)
	}

	// Declare the lowerings.
	g.add(
		gImport{iface: "wasi:sockets/tcp-create-socket@0.2.0", name: "create-tcp-socket", kind: gMem, params: composeTcpCreateParams},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[method]tcp-socket.start-bind", kind: gMem, params: composeTcpStartBindParams},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[method]tcp-socket.finish-bind", kind: gMem, params: composeTcpSelfRetParams},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[method]tcp-socket.start-listen", kind: gMem, params: composeTcpSelfRetParams},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[method]tcp-socket.finish-listen", kind: gMem, params: composeTcpSelfRetParams},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[method]tcp-socket.accept", kind: gMem, params: composeTcpSelfRetParams},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[method]tcp-socket.subscribe", kind: gNoOpt},
		gImport{iface: "wasi:sockets/tcp@0.2.0", name: "[resource-drop]tcp-socket", kind: gDrop, resourceT: g.surfaced["tcp-socket"]},
		gImport{iface: "wasi:io/poll@0.2.0", name: "[method]pollable.block", kind: gNoOpt},
		gImport{iface: "wasi:io/poll@0.2.0", name: "[resource-drop]pollable", kind: gDrop, resourceT: g.surfaced["pollable"]},
		gImport{iface: "wasi:io/streams@0.2.0", name: "[resource-drop]input-stream", kind: gDrop, resourceT: g.surfaced["input-stream"]},
		gImport{iface: "wasi:io/streams@0.2.0", name: "[resource-drop]output-stream", kind: gDrop, resourceT: g.surfaced["output-stream"]},
		gImport{iface: "wasi:sockets/instance-network@0.2.0", name: "instance-network", kind: gNoOpt},
	)
	if hasStreamWrite {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams})
	}
	if hasStreamRead {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: composeBlockReadName, kind: gMemRealloc, params: composeBlockReadParams})
	}
	if hasFile {
		g.add(
			gImport{iface: "wasi:filesystem/preopens@0.2.0", name: composeGetDirsName, kind: gMemRealloc, params: composeGetDirsParams},
			gImport{iface: "wasi:filesystem/types@0.2.0", name: composeOpenAtName, kind: gMem, params: composeOpenAtParams},
			gImport{iface: "wasi:filesystem/types@0.2.0", name: viaName, kind: gMem, params: viaParams},
		)
	}
	for _, mt := range extras {
		kind := gMem
		if mt.NeedsRealloc {
			kind = gMemRealloc
		}
		g.add(gImport{iface: mt.InterfaceName, name: mt.FuncName, kind: kind, params: composeOneI32Params})
	}
	if hasStdout {
		g.add(gImport{iface: "wasi:cli/stdout@0.2.0", name: "get-stdout", kind: gNoOpt})
	}
	if hasStderr {
		g.add(gImport{iface: "wasi:cli/stderr@0.2.0", name: "get-stderr", kind: gNoOpt})
	}
	if hasStdin {
		g.add(gImport{iface: "wasi:cli/stdin@0.2.0", name: "get-stdin", kind: gNoOpt})
	}
	for _, imp := range structured {
		g.add(gImport{iface: imp.InterfaceName, name: imp.FuncName, kind: gNoOpt})
	}

	return g.finish(coreBytes, coreExportName, "")
}
