// TCP-socket runtime helpers for the wasmbin backend.
//
// The user-facing `tcp_listen(port)` / `tcp_accept(listener)` /
// `tcp_recv(conn, max)` / `tcp_send(conn, data)` / `tcp_close(conn)`
// builtins lower to OpCallDirect with the bare names; the IR alias
// table routes those to the synthetic helpers in this file. The
// helpers go through wasi:sockets + wasi:io directly — no preview-1
// adapter — so a module that touches TCP must be composed against
// a preview-2-capable host (wasmtime serve, jco transpile, etc.).
//
// Layout of a connection / listener struct:
//
//	[0..3]  tcp-socket handle
//	[4..7]  input-stream handle  (0 for listening sockets)
//	[8..11] output-stream handle (0 for listening sockets)
//
//	[12..15] streams present: 0 for listeners, 1 for connections
//
// Total 16 bytes per struct. Resource handle zero is valid. The presence
// word distinguishes absent streams from live streams with handle zero.
// tcp_close drops child streams before their parent socket.
//
// Return-pointer (retptr) buffers are sized to fit the canonical-
// ABI flattening of each call's `result<...>`:
//
//	create-tcp-socket  → 8 bytes  (1 disc + 1 socket-or-errno)
//	start-bind /
//	  finish-bind /
//	  start-listen /
//	  finish-listen   → 8 bytes  (1 disc + 1 errno; payload is unit)
//	accept            → 16 bytes (1 disc + 3 socket/stream/stream OR 1 errno)
//	blocking-read     → 12 bytes (1 disc + 1 list-data + 1 list-len)
//	blocking-write-
//	  and-flush       → 4 bytes  (1 disc; payload is unit on both arms)
//
// Each helper allocates a fresh retptr via __fern_alloc. The
// bump allocator can't free, so the buffers leak — bounded by
// the per-process call count, and acceptable for the edge-handler
// workload the language targets.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// buildNetworkHandleBody assembles __network_handle.
//
// Signature: () → i32 (network handle).
//
// Lazily fetches the wasi:sockets/instance-network handle on
// first call and caches it at networkHandleAddr; a separate
// init flag at networkHandleInitAddr handles the 0-is-valid
// case (resource handles are opaque ints where 0 may be a
// real handle, so a 0-sentinel on the handle slot itself
// wouldn't disambiguate).
//
// Logical:
//
//	if mem[networkHandleInitAddr] != 0 {
//	    return mem[networkHandleAddr]
//	} else {
//	    h = wasi_sockets_instance_network()
//	    mem[networkHandleAddr] = h
//	    mem[networkHandleInitAddr] = 1
//	    return h
//	}
//
// Locals: none — the if-then-else result-shape carries the
// returned value through both arms.
func buildNetworkHandleBody(idxs map[string]uint32) []byte {
	instanceNetwork := idxs["wasi_sockets_instance_network"]
	var body []byte
	body = inst.InstI32Const(body, networkHandleInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	{
		body = inst.InstI32Const(body, networkHandleAddr)
		body = memory.InstI32Load(body, 2, 0)
	}
	body = inst.InstElse(body)
	{
		// $h = instance-network(); cache + flag set; return $h.
		// instance-network leaves the handle on the stack; spill
		// into local 0 so we can store it and re-push for the
		// if's result.
		body = inst.InstCall(body, instanceNetwork)
		body = inst.InstLocalSet(body, 0)
		// mem[networkHandleAddr] = $h
		body = inst.InstI32Const(body, networkHandleAddr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Store(body, 2, 0)
		// mem[networkHandleInitAddr] = 1
		body = inst.InstI32Const(body, networkHandleInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		// Result of else arm: $h.
		body = inst.InstLocalGet(body, 0)
	}
	body = inst.InstEnd(body)
	// One i32 local for the handle cache shuffle.
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// emitErrnoNegReturn loads the socket error code from retptr+4 and returns
// its negative Preview 1 errno. Used after every wasi:sockets call that lands a
// result<_, error-code> at retptr: byte 0 holds the discriminant
// and byte 4 holds the error-code variant value (a u8 enum).
// Discriminant zero is unknown, not success. Translate before negating.
//
// Stack on entry: empty. Stack on exit: function has returned.
func emitErrnoNegReturn(body []byte, retptrLocal uint32, idxs map[string]uint32) []byte {
	return emitErrnoNegReturnReclaim(body, retptrLocal, 0, idxs)
}

// Save the errno on the operand stack before free overwrites the return area.
func emitErrnoNegReturnReclaim(body []byte, retptrLocal uint32, size int32, idxs map[string]uint32) []byte {
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, retptrLocal)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstCall(body, idxs["__fern_wasi_socket_errno"])
	body = numeric.InstI32Sub(body)
	if size != 0 {
		body = inst.InstLocalGet(body, retptrLocal)
		body = inst.InstI32Const(body, size)
		body = inst.InstCall(body, idxs["__free"])
	}
	body = inst.InstReturn(body)
	return body
}

// buildTcpListenBody assembles __fern_tcp_listen.
//
// Signature: (port: i32) → i32 — heap pointer to a 16-byte
// listener struct on success, or -errno (negative int) on
// failure. Matches the WAT contract; the lang surface treats
// values < 0 as failed.
//
// Pipeline: create-tcp-socket(ipv4) → start-bind +
// finish-bind(0.0.0.0:port) → start-listen + finish-listen.
// Each canonical-ABI call lands its result at a fresh
// retptr scratch; we check the discriminant byte and bail on
// the first non-zero (Err) variant.
//
// start-bind's 15 i32 params are the canonical-ABI flattening
// of `ip-socket-address`: 1 disc + an 11-i32 max payload
// (ipv4 uses 5 slots, ipv6 fills the rest; the variant joins
// them). We always emit the ipv4 case bound to 0.0.0.0:port
// with the trailing 6 slots zero-padded.
//
// Locals (after the one param):
//
//	1: $sock       — tcp-socket handle (Ok arm of create-tcp-socket)
//	2: $retptr     — 16-byte retptr scratch (oversized for the
//	                 wider accept retptr, but tcp_listen doesn't
//	                 need it that big; 8 would suffice. Keep 16
//	                 for symmetry with the rest of the file.)
//	3: $struct     — heap-allocated 16-byte listener struct
func buildTcpListenBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	netHandle := idxs["__network_handle"]
	createSock := idxs["wasi_sockets_create_tcp_socket"]
	startBind := idxs["wasi_sockets_tcp_start_bind"]
	finishBind := idxs["wasi_sockets_tcp_finish_bind"]
	startListen := idxs["wasi_sockets_tcp_start_listen"]
	finishListen := idxs["wasi_sockets_tcp_finish_listen"]

	// After create succeeds, every failed setup step owns this socket,
	// including a zero-valued resource handle.
	fail := func(body []byte) []byte {
		body = inst.InstLocalGet(body, 1)
		body = inst.InstCall(body, idxs["wasi_sockets_tcp_socket_drop"])
		return emitErrnoNegReturnReclaim(body, 2, 16, idxs)
	}

	var body []byte

	// The 16-byte return area becomes the owned record after successful setup.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// create-tcp-socket(ipv4=0, retptr).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, createSock)
	// if disc != 0: return -errno
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 2, 16, idxs)
	body = inst.InstEnd(body)
	// $sock = mem[retptr + 4]
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 1)

	// start-bind(self=$sock, borrow<network>, disc=0 (ipv4),
	//   ipv4_port=$port, 4 ipv4 bytes (0.0.0.0), 6 padding slots,
	//   retptr).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, netHandle)
	body = inst.InstI32Const(body, 0) // disc = 0 (ipv4)
	body = inst.InstLocalGet(body, 0) // port
	body = inst.InstI32Const(body, 0) // ipv4 byte 0
	body = inst.InstI32Const(body, 0) // ipv4 byte 1
	body = inst.InstI32Const(body, 0) // ipv4 byte 2
	body = inst.InstI32Const(body, 0) // ipv4 byte 3
	body = inst.InstI32Const(body, 0) // pad 1
	body = inst.InstI32Const(body, 0) // pad 2
	body = inst.InstI32Const(body, 0) // pad 3
	body = inst.InstI32Const(body, 0) // pad 4
	body = inst.InstI32Const(body, 0) // pad 5
	body = inst.InstI32Const(body, 0) // pad 6 — total 6 padding slots
	body = inst.InstLocalGet(body, 2) // retptr
	body = inst.InstCall(body, startBind)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body)
	body = inst.InstEnd(body)

	// finish-bind(self, retptr).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, finishBind)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body)
	body = inst.InstEnd(body)

	// start-listen(self, retptr).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, startListen)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body)
	body = inst.InstEnd(body)

	// finish-listen(self, retptr).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, finishListen)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body)
	body = inst.InstEnd(body)

	// Reuse the 16-byte return area as the owned socket record.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstLocalGet(body, 1) // $sock
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // input-stream slot = 0
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // output-stream slot = 0
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 0) // streams-present, independent of handle values
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 3)

	// Three i32 locals after the one param: $sock, $retptr, $struct.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTcpConnectBody assembles __fern_tcp_connect — the outbound
// client.
//
// Signature: (host_be: i32, port: i32) → i32
//
// host_be is the IPv4 address packed a | b<<8 | c<<16 | d<<24 (the
// std/fetch `ipv4` convention), unpacked here into the four octets of
// the ip-socket-address ipv4 form. Pipeline: create-tcp-socket →
// start-connect(remote addr) → subscribe → pollable.block (wait for
// the connection to establish) → pollable.drop → finish-connect.
// Returns a 16-byte connection struct (tcp-socket, input-stream,
// output-stream) — the SAME shape tcp_accept yields, so tcp_recv /
// tcp_send / tcp_close work on it unchanged — or -errno on failure.
//
// Locals (params 0 = host_be, 1 = port):
//
//	2: $sock   3: $retptr   4: $struct   5: $pollable
func buildTcpConnectBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	netHandle := idxs["__network_handle"]
	createSock := idxs["wasi_sockets_create_tcp_socket"]
	startConnect := idxs["wasi_sockets_tcp_start_connect"]
	finishConnect := idxs["wasi_sockets_tcp_finish_connect"]
	subscribe := idxs["wasi_sockets_tcp_subscribe"]
	pollBlock := idxs["wasi_io_pollable_block"]
	pollDrop := idxs["wasi_io_pollable_drop"]

	octet := func(body []byte, shift int32) []byte {
		body = inst.InstLocalGet(body, 0)
		if shift != 0 {
			body = inst.InstI32Const(body, shift)
			body = numeric.InstI32ShrU(body)
		}
		body = inst.InstI32Const(body, 0xff)
		return numeric.InstI32And(body)
	}

	// After create succeeds, every failed setup step owns this socket,
	// including a zero-valued resource handle.
	fail := func(body []byte) []byte {
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, idxs["wasi_sockets_tcp_socket_drop"])
		return emitErrnoNegReturnReclaim(body, 3, 16, idxs)
	}

	var body []byte

	// $retptr = alloc(16).
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)

	// create-tcp-socket(ipv4=0, retptr).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, createSock)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 3, 16, idxs)
	body = inst.InstEnd(body)
	// $sock = mem[retptr + 4].
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)

	// start-connect(self=$sock, network, disc=0 (ipv4), port,
	//   4 ipv4 octets from host_be, 6 padding slots, retptr).
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, netHandle)
	body = inst.InstI32Const(body, 0) // disc = ipv4
	body = inst.InstLocalGet(body, 1) // port
	body = octet(body, 0)             // a = host_be & 0xff
	body = octet(body, 8)             // b
	body = octet(body, 16)            // c
	body = octet(body, 24)            // d
	body = inst.InstI32Const(body, 0) // pad 1
	body = inst.InstI32Const(body, 0) // pad 2
	body = inst.InstI32Const(body, 0) // pad 3
	body = inst.InstI32Const(body, 0) // pad 4
	body = inst.InstI32Const(body, 0) // pad 5
	body = inst.InstI32Const(body, 0) // pad 6
	body = inst.InstLocalGet(body, 3) // retptr
	body = inst.InstCall(body, startConnect)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body)
	body = inst.InstEnd(body)

	// subscribe($sock) → $pollable; block until connected; drop it.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, subscribe)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, pollBlock)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, pollDrop)

	// finish-connect(self, retptr).
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, finishConnect)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body)
	body = inst.InstEnd(body)

	// Reuse the 16-byte return area as the owned socket record.
	// finish-connect's Ok payload is tuple<input @ retptr+4,
	// output @ retptr+8>.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalTee(body, 4)
	body = inst.InstLocalGet(body, 2) // $sock
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0) // input-stream @ retptr+4
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0) // output-stream @ retptr+8
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 1) // streams-present, independent of handle values
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 4)

	// Four i32 locals after the two params: $sock, $retptr, $struct, $pollable.
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTcpPollableBody assembles __fern_tcp_pollable.
//
// Signature: (conn: i32) → i32
//
// Returns a wasi:io/poll pollable for the connection's tcp-socket
// (mem[conn+0]) via tcp-socket.subscribe — the handle std/async
// multiplexes through wasm_poll for overlapped outbound fan-out.
func buildTcpPollableBody(idxs map[string]uint32) []byte {
	subscribe := idxs["wasi_sockets_tcp_subscribe"]
	var body []byte
	body = inst.InstLocalGet(body, 0)     // $conn
	body = memory.InstI32Load(body, 2, 0) // tcp-socket @ conn+0
	body = inst.InstCall(body, subscribe) // → pollable handle
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildTcpLocalPortBody assembles __fern_tcp_local_port.
//
// Signature: (sock: i32) → i32 — the port mem[sock+0] is bound to,
// or -errno. The answer for a `tcp_listen(0)`, whose port the host
// picked and nothing else reports.
//
// `local-address` returns `result<ip-socket-address, error-code>`.
// Laid out in memory that is 1 disc byte at +0, 3 bytes pad, then
// the ip-socket-address variant at +4: its own disc at +4 (ipv4 / ipv6)
// and its payload at +8, since the widest case (ipv6-socket-address)
// aligns to 4. `port` is the first field of BOTH cases, so the u16 at
// +8 is the port whichever address family the host answered with —
// no branch on the variant tag. Largest case is the 28-byte ipv6
// address, so the retptr is 4 + 4 + 28 = 36 bytes.
//
// Locals (after the one param):
//
//	1: $sock   — tcp-socket handle (mem[$sock])
//	2: $retptr — 36-byte retptr scratch
func buildTcpLocalPortBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	localAddress := idxs["wasi_sockets_tcp_local_address"]

	var body []byte

	// $sock = mem[$sockstruct]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 1)

	// $retptr = alloc(36); local-address($sock, $retptr).
	body = inst.InstI32Const(body, 36)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, localAddress)

	// Err arm: return -errno.
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturn(body, 2, idxs)
	body = inst.InstEnd(body)

	// Ok arm: the port, a u16 at retptr+8 in linear-memory order.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load16U(body, 1, 0)

	// 2 i32 locals after the 1 param.
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTcpAcceptBody assembles __fern_tcp_accept.
//
// Signature: (listener: i32) → i32 — heap pointer to a fresh
// 16-byte connection struct on success, -errno on failure.
//
// Pipeline: subscribe(sock) → pollable.block → accept. The
// `accept` result is `result<tuple<tcp-socket, input-stream,
// output-stream>, error-code>`, lowered to 16 bytes (1 disc +
// 3 pad + 12 payload). Allocate 16 bytes for retptr.
//
// We subscribe + block first because accept is non-blocking on
// wasi:sockets; without the poll we'd just get would-block on
// the first call.
//
// Locals (after the one param):
//
//	1: $sock      — listener tcp-socket handle (mem[$listener])
//	2: $pollable  — pollable handle from tcp-socket.subscribe
//	3: $retptr    — 16-byte retptr scratch
//	4: $newsock   — accepted tcp-socket handle (Ok payload slot 0)
//	5: $instream  — input-stream handle (Ok payload slot 1)
//	6: $outstream — output-stream handle (Ok payload slot 2)
//	7: $struct    — 16-byte connection struct
func buildTcpAcceptBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	subscribe := idxs["wasi_sockets_tcp_subscribe"]
	pollBlock := idxs["wasi_io_pollable_block"]
	pollDrop := idxs["wasi_io_pollable_drop"]
	accept := idxs["wasi_sockets_tcp_accept"]

	var body []byte

	// $sock = mem[$listener]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 1)

	// $pollable = subscribe($sock); pollable.block($pollable);
	// pollable.drop($pollable).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, subscribe)
	body = inst.InstLocalTee(body, 2)
	body = inst.InstCall(body, pollBlock)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, pollDrop)

	// $retptr = alloc(16); accept($sock, $retptr).
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, accept)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 3, 16, idxs)
	body = inst.InstEnd(body)

	// Ok payload at retptr+4: (tcp-socket, input-stream, output-stream).
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4) // $newsock
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5) // $instream
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 6) // $outstream

	// Reuse the 16-byte return area as the owned socket record.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalTee(body, 7)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 6)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 1) // streams-present, independent of handle values
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 7)

	// 7 i32 locals after the 1 param.
	locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTcpRecvBody assembles __fern_tcp_recv.
//
// Signature: (conn: i32, max: i32) → i32 — a u8[] data pointer
// in the __alloc_u8 box shape (16-byte cap/rc/len header behind
// the data pointer; D9, #5714). Stream-error, EOF, and max <= 0
// all return the empty box __alloc_u8(0), the same sentinel the
// empty string was when this returned a (data, len) pair.
//
// Pipeline: load the input-stream handle from the connection
// struct (`mem[$conn + 4]`), call blocking-read(stream, max,
// retptr=12B), copy the host's list<u8> payload into a fresh
// __alloc_u8 box, return the box's data pointer. __alloc_u8(n)
// writes the length prefix, so the copy is all that remains.
//
// Locals (after the two params):
//
//	2: $stream   — input-stream handle (mem[$conn + 4])
//	3: $retptr   — 12-byte retptr scratch
//	4: $list_ptr — list<u8> data pointer (Ok payload slot 0)
//	5: $n        — list<u8> length (Ok payload slot 1)
//	6: $arr      — __alloc_u8 box holding the read bytes
func buildTcpRecvBody(idxs map[string]uint32) []byte {
	// The u8[] result carries the cap/rc/len header only when it comes from
	// __alloc_u8; the 12-byte retptr scratch is not an array, so it stays on
	// plain __fern_alloc.
	alloc := idxs["__fern_alloc"]
	allocU8 := idxs["__alloc_u8"]
	blockingRead := idxs["wasi_io_blocking_read"]

	var body []byte

	// max <= 0 → empty box.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LeS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, allocU8)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// $stream = mem[$conn + 4]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)

	// $retptr = alloc(12) — 1 disc + 3 pad + 4 list-data + 4 list-len.
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)

	// blocking-read($stream, (i64)$max, $retptr).
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = convert.InstI64ExtendI32U(body)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, blockingRead)

	// On Err (stream-error or closed/EOF), return the empty box.
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, allocU8)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Ok payload: list_ptr @ retptr+4, list_len @ retptr+8.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)

	// $arr = __alloc_u8($n); memory.copy(arr, list_ptr, n).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, allocU8)
	body = inst.InstLocalTee(body, 6)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstMemoryCopy(body)

	// Return the box's data pointer.
	body = inst.InstLocalGet(body, 6)

	// 5 i32 locals after the 2 params.
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTcpSendBody assembles __fern_tcp_send.
//
// Signature: (conn: i32, data_data: i32, data_len: i32) → i32 —
// the byte count on success, -1 on stream-error. The preview-1
// path surfaced -errno; preview-2 stream errors don't carry an
// errno number through the canonical-ABI variant we use, so -1
// is the best negative sentinel.
//
// Pipeline: SSO-normalize the input string into a heap buffer
// (so inline-form strings get a real contiguous byte buffer
// for the host to read), load the output-stream handle from the
// connection struct (`mem[$conn + 8]`), then loop
// blocking-write-and-flush in 4096-byte chunks until the whole
// buffer drains.
//
// Wasmtime caps blocking-write-and-flush at 4 KiB per call so
// long payloads need the chunked loop. Stream errors mid-flight
// short-circuit to a -1 return.
//
// Locals (after the three params):
//
//	3: $stream    — output-stream handle (mem[$conn + 8])
//	4: $retptr    — 4-byte retptr scratch (disc-only variant)
//	5: $buf       — SSO-normalized data buffer
//	6: $byte_len  — decoded byte length of the data string
//	7: $i_norm    — emitStrNormalize loop counter
//	8: $off       — bytes-written-so-far cursor
//	9: $chunk     — bytes to write this iteration (≤ 4096)
func buildTcpSendBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	blockingWrite := idxs["wasi_blocking_write_and_flush_p2"]

	var body []byte

	// $stream = mem[$conn + 8]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)

	// SSO-normalize the data into a contiguous heap buffer the
	// host can dereference. Inline-form strings (high bit of
	// data_len set) pack their bytes into the (data_data,
	// data_len) bit pattern itself; that's not a memory
	// address so blocking-write-and-flush would read garbage.
	body = emitStrNormalize(body, idxs, 1, 2, 5, 6, 7)

	// $retptr = alloc(4) — result<_, stream-error> flattens to a
	// single disc byte; payload on Err is the stream-error
	// resource (we ignore it).
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)

	// Chunked write loop. $off = 0.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $off >= $byte_len, break.
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)

		// $chunk = $byte_len - $off (clamped to 4096 below).
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 8)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalTee(body, 9)
		body = inst.InstI32Const(body, 4096)
		body = numeric.InstI32GtU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, 4096)
			body = inst.InstLocalSet(body, 9)
		}
		body = inst.InstEnd(body)

		// blocking-write-and-flush($stream, $buf + $off, $chunk, $retptr).
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, blockingWrite)

		// If disc != 0 (Err), return -1.
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, -1)
			body = inst.InstReturn(body)
		}
		body = inst.InstEnd(body)

		// $off += $chunk; continue.
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 9)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 8)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// Return $byte_len (the requested length, which equals the
	// bytes actually written when the loop drained without error).
	body = inst.InstLocalGet(body, 6)

	// 7 i32 locals after the 3 params.
	locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTcpCloseBody assembles __fern_tcp_close.
//
// Signature: (conn: i32) → i32 — always 0. Resource drops are
// infallible at the canonical-ABI layer, so the return value
// is a fixed sentinel; the i32 return type matches the lang
// surface contract for symmetry with the other tcp_* helpers.
//
// Drop order: input-stream + output-stream FIRST, then the
// parent tcp-socket. The canonical-ABI rejects parent drops
// with live children ("resource has children" error), so the
// stream slots must be released before their owning socket.
//
// Offset 12 records whether the socket owns streams. Every u32 resource
// handle, including zero, is valid; handle values cannot encode absence.
func buildTcpCloseBody(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 12)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstCall(body, idxs["wasi_io_input_stream_drop"])
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstCall(body, idxs["wasi_io_output_stream_drop"])
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstCall(body, idxs["wasi_sockets_tcp_socket_drop"])
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstI32Const(body, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 0, encode.ValtypeI32), body)
}
