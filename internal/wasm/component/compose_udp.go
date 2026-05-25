package component

// compose_udp.go composes a wasi:cli/run component for a send-only UDP
// client (udp_send), on the general composition engine (compose_general.go).
// The datagram path is its own resources (not wasi:io/streams); a
// fire-and-forget send never blocks on a pollable. Every udp method
// lowers as a memory trampoline (retptr result / a list param the host
// reads) with no realloc; instance-network is a no-opt; the three
// datagram resources drop via canon resource.drop. Optional CLI extras
// (print/eprint, now/env/args/exit/random/monotonic) ride the engine's
// shared surfacing.

var (
	udpCreateParams     = []byte{0x7f, 0x7f} // (family, retptr)
	udpSelfRetParams    = []byte{0x7f, 0x7f} // (self, retptr)
	udpBindStreamParams = repeatI32(15)      // self, (option<ip-socket-address> flat = 13), retptr
	udpSendParams       = repeatI32(4)       // self, list_ptr, list_len, retptr
)

// ComposeUdpClientCliRun wraps a send-only UDP core into a wasi:cli/run
// component. hasStdout / hasStderr add print/eprint logging; extras /
// structured add the standalone CLI capabilities.
func ComposeUdpClientCliRun(coreBytes []byte, hasStdout, hasStderr bool, extras []MemTrampImport, structured []WasiImport, coreExportName string) []byte {
	g := newGComposer()

	// Surface + import the socket surface (network → udp → create) and
	// instance-network; then the optional CLI extras.
	g.ensureUdpCreate()
	g.ensureInstanceNetwork()
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

	// Declare the lowerings.
	g.add(
		gImport{iface: "wasi:sockets/udp-create-socket@0.2.0", name: "create-udp-socket", kind: gMem, params: udpCreateParams},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[method]udp-socket.start-bind", kind: gMem, params: udpBindStreamParams},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[method]udp-socket.finish-bind", kind: gMem, params: udpSelfRetParams},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[method]udp-socket.stream", kind: gMem, params: udpBindStreamParams},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[method]outgoing-datagram-stream.check-send", kind: gMem, params: udpSelfRetParams},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[method]outgoing-datagram-stream.send", kind: gMem, params: udpSendParams},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[resource-drop]udp-socket", kind: gDrop, resourceT: g.surfaced["udp-socket"]},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[resource-drop]incoming-datagram-stream", kind: gDrop, resourceT: g.surfaced["incoming-datagram-stream"]},
		gImport{iface: "wasi:sockets/udp@0.2.0", name: "[resource-drop]outgoing-datagram-stream", kind: gDrop, resourceT: g.surfaced["outgoing-datagram-stream"]},
		gImport{iface: "wasi:sockets/instance-network@0.2.0", name: "instance-network", kind: gNoOpt},
	)
	if hasStdout || hasStderr {
		g.add(gImport{iface: "wasi:io/streams@0.2.0", name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams})
	}
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

	return g.finish(coreBytes, coreExportName, "")
}
