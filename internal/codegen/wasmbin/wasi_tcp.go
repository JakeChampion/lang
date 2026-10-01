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
// Setup reuses its return area as the successful socket record, reclaimed
// by close, or frees it on failure. Local-port queries free their return
// area on both paths. Stream-I/O scratch reclamation remains separate work.

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

// emitErrnoNegReturn loads the socket error code from retptr+errAt and
// returns its negative Preview 1 errno. Used after every wasi:sockets call
// that lands a result<..., error-code> at retptr: byte 0 holds the
// discriminant and the error-code (a u8 enum) sits at the payload offset
// the canonical ABI gives the result's widest case: errAt is 1 for a
// `result<_, error-code>` (bind, listen, connect-start, the socket
// controls), 4 when the ok case is a handle, a tuple of handles or an
// address, and 8 for a u64 count. Discriminant zero is unknown, not
// success. Translate before negating.
//
// Stack on entry: empty. Stack on exit: function has returned.
func emitErrnoNegReturn(body []byte, retptrLocal, errAt uint32, idxs map[string]uint32) []byte {
	return emitErrnoNegReturnReclaim(body, retptrLocal, errAt, 0, idxs)
}

// Save the errno on the operand stack before free overwrites the return area.
func emitErrnoNegReturnReclaim(body []byte, retptrLocal, errAt uint32, size int32, idxs map[string]uint32) []byte {
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, retptrLocal)
	body = memory.InstI32Load8U(body, 0, errAt)
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
		return emitErrnoNegReturnReclaim(body, 2, 1, 16, idxs)
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
	body = emitErrnoNegReturnReclaim(body, 2, 4, 16, idxs)
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

// emitIsUdpRecord pushes whether the socket record in local `rec` is a
// datagram socket's: kind 2 or 3 (wasi_udp.go).
func emitIsUdpRecord(body []byte, rec uint32) []byte {
	body = inst.InstLocalGet(body, rec)
	body = memory.InstI32Load(body, 2, 12)
	body = inst.InstI32Const(body, udpRecordBare)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 2)
	return numeric.InstI32LtU(body)
}

// tcpRecordConnecting marks a connection record whose connect
// tcp_connect_with only started: the socket at +0, no streams, until
// control op 5 finishes it and the record becomes a connection (1).
const tcpRecordConnecting = 4

// buildTcpConnectBody assembles __fern_tcp_connect — the outbound
// client: __fern_tcp_connect_with without the non-blocking flag, the
// packed IPv4 address (a | b<<8 | c<<16 | d<<24) boxed as the u8[] it
// takes.
//
// Locals (params 0 = host_be, 1 = port):
//
//	2: $box   the fixed box's data pointer (wasi_addr.go)
func buildTcpConnectBody(idxs map[string]uint32) []byte {
	var body []byte
	body = emitIpBox(body, 2, ipBoxAddr, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, idxs["__fern_tcp_connect_with"])
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32), body)
}

// buildTcpConnectWithBody assembles __fern_tcp_connect_with.
//
// Signature: (addr: i32, port: i32, nonblocking: i32) → i32
//
// addr is the u8[] of the address's network-order bytes, flattened by
// __fern_ip_flat into the ip-socket-address the host takes (wasi_addr.go).
// Pipeline: create-tcp-socket → start-connect(remote addr) → subscribe →
// pollable.block (wait for the connection to establish) → pollable.drop
// → finish-connect.
// Returns a 16-byte connection struct (tcp-socket, input-stream,
// output-stream) — the SAME shape tcp_accept yields, so tcp_recv /
// tcp_send / tcp_close work on it unchanged — or -errno on failure.
//
// With `nonblocking` the pipeline stops after start-connect and the
// record goes back marked tcpRecordConnecting; control op 5 runs
// finish-connect on it.
//
// Locals (params 0 = addr, 1 = port, 2 = nonblocking):
//
//	3: $sock   4: $retptr   5: $struct   6: $pollable   7: $flat   8: $tmp
func buildTcpConnectWithBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	netHandle := idxs["__network_handle"]
	createSock := idxs["wasi_sockets_create_tcp_socket"]
	startConnect := idxs["wasi_sockets_tcp_start_connect"]
	finishConnect := idxs["wasi_sockets_tcp_finish_connect"]
	subscribe := idxs["wasi_sockets_tcp_subscribe"]
	pollBlock := idxs["wasi_io_pollable_block"]
	pollDrop := idxs["wasi_io_pollable_drop"]

	// After create succeeds, every failed setup step owns this socket,
	// including a zero-valued resource handle.
	fail := func(body []byte, errAt uint32) []byte {
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, idxs["wasi_sockets_tcp_socket_drop"])
		return emitErrnoNegReturnReclaim(body, 4, errAt, 16, idxs)
	}

	var body []byte
	body = emitIpFlat(body, idxs, 0, 1, 7, 8)

	// $retptr = alloc(16).
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)

	// create-tcp-socket(family, retptr).
	body = inst.InstLocalGet(body, 7)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, createSock)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 4, 4, 16, idxs)
	body = inst.InstEnd(body)
	// $sock = mem[retptr + 4].
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)

	// start-connect(self=$sock, network, the flattened address, retptr).
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, netHandle)
	body = emitIpFlatWords(body, 7)
	body = inst.InstLocalGet(body, 4) // retptr
	body = inst.InstCall(body, startConnect)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body, 1)
	body = inst.InstEnd(body)

	// Non-blocking: the record goes back with the connect under way and
	// control op 5 finishes it.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, tcpRecordConnecting)
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// subscribe($sock) → $pollable; block until connected; drop it.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, subscribe)
	body = inst.InstLocalSet(body, 6)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, pollBlock)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, pollDrop)

	// finish-connect(self, retptr).
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, finishConnect)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body, 4)
	body = inst.InstEnd(body)

	// Reuse the 16-byte return area as the owned socket record.
	// finish-connect's Ok payload is tuple<input @ retptr+4,
	// output @ retptr+8>.
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalTee(body, 5)
	body = inst.InstLocalGet(body, 3) // $sock
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0) // input-stream @ retptr+4
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0) // output-stream @ retptr+8
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 1) // streams-present, independent of handle values
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 5)

	// Six i32 locals after the three params: $sock, $retptr, $struct,
	// $pollable, $flat, $tmp.
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
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

	// $retptr = alloc(36); local-address($sock, $retptr), through the
	// udp method when the record is a datagram socket's.
	body = inst.InstI32Const(body, 36)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	query := func(body []byte, method uint32) []byte {
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 2)
		return inst.InstCall(body, method)
	}
	if udpLocalAddress, ok := idxs["wasi_sockets_udp_local_address"]; ok {
		body = emitIsUdpRecord(body, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = query(body, udpLocalAddress)
		body = inst.InstElse(body)
		body = query(body, localAddress)
		body = inst.InstEnd(body)
	} else {
		body = query(body, localAddress)
	}

	// Err arm: return -errno.
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 2, 4, 36, idxs)
	body = inst.InstEnd(body)

	// Ok arm: the port, a u16 at retptr+8 in linear-memory order.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load16U(body, 1, 0)
	// Keep the scalar result on the stack while returning its scratch area.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 36)
	body = inst.InstCall(body, idxs["__free"])

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
	body = emitErrnoNegReturnReclaim(body, 3, 4, 16, idxs)
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
	freeRet := func(body []byte) []byte {
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 12)
		return inst.InstCall(body, idxs["__free"])
	}

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
	body = emitStreamErrorDrop(body, idxs, 3)
	body = freeRet(body)
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
	// The canonical list was allocated in our heap by cabi_realloc. Its
	// copied array now owns the bytes; empty lists own no allocation.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstEnd(body)
	body = freeRet(body)

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
//	4: $retptr    — 12-byte result<_, stream-error> return area
//	5: $buf       — SSO-normalized data buffer
//	6: $byte_len  — decoded byte length of the data string
//	7: $i_norm    — emitStrNormalize loop counter
//	8: $off       — bytes-written-so-far cursor
//	9: $chunk     — bytes to write this iteration (≤ 4096)
func buildTcpSendBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	blockingWrite := idxs["wasi_blocking_write_and_flush_p2"]
	reclaim := func(body []byte) []byte {
		body = emitStrNormalizeFree(body, idxs, 2, 5, 6)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 12)
		return inst.InstCall(body, idxs["__free"])
	}

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

	// result<_, stream-error>: outer tag at 0, error tag at 4 and
	// last-operation-failed's owned error handle at 8.
	body = inst.InstI32Const(body, 12)
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
			body = emitStreamErrorDrop(body, idxs, 4)
			body = reclaim(body)
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
	body = reclaim(body)

	// 7 i32 locals after the 3 params.
	locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// Called only for the Err arm. Closed (tag 1) owns nothing; tag 0 owns an
// io/error resource whose handle may itself be zero.
func emitStreamErrorDrop(body []byte, idxs map[string]uint32, ret uint32) []byte {
	body = inst.InstLocalGet(body, ret)
	body = memory.InstI32Load8U(body, 0, 4)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, ret)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstCall(body, idxs["wasi_io_error_drop"])
	return inst.InstEnd(body)
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
// Offset 12 records whether the socket owns streams (1; a listener is 0 and
// a connect under way 4), and marks a datagram socket (wasi_udp.go), whose
// resources drop through the udp imports.
// Every u32 resource handle, including zero, is valid; handle values
// cannot encode absence.
func buildTcpCloseBody(idxs map[string]uint32) []byte {
	var body []byte
	tcp := func(body []byte) []byte {
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 12)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Eq(body)
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
		return inst.InstCall(body, idxs["wasi_sockets_tcp_socket_drop"])
	}
	if udpClose, ok := idxs["__fern_udp_close"]; ok {
		body = emitIsUdpRecord(body, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstCall(body, udpClose)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
	}
	body = tcp(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstI32Const(body, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 0, encode.ValtypeI32), body)
}

// buildTcpListenWithBody assembles __fern_tcp_listen_with: the listener
// __fern_tcp_listen builds, bound to the address in the u8[] at addr
// (wasi_addr.go) rather than 0.0.0.0, with set-listen-backlog-size(backlog)
// between the bind and the listen. reuse_port has no wasi:sockets control
// and is not read; on this target a port is one socket's.
//
// Signature: (addr, port, backlog, reuse_port: i32) → i32.
//
// Locals (after the four params):
//
//	4: $sock   5: $retptr (16 bytes, becomes the record)   6: $flat   7: $tmp
func buildTcpListenWithBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	netHandle := idxs["__network_handle"]
	createSock := idxs["wasi_sockets_create_tcp_socket"]
	startBind := idxs["wasi_sockets_tcp_start_bind"]
	finishBind := idxs["wasi_sockets_tcp_finish_bind"]
	setBacklog := idxs["wasi_sockets_tcp_set_listen_backlog_size"]
	startListen := idxs["wasi_sockets_tcp_start_listen"]
	finishListen := idxs["wasi_sockets_tcp_finish_listen"]
	const sock, retptr, flat, tmp = 4, 5, 6, 7

	fail := func(body []byte) []byte {
		body = inst.InstLocalGet(body, sock)
		body = inst.InstCall(body, idxs["wasi_sockets_tcp_socket_drop"])
		return emitErrnoNegReturnReclaim(body, retptr, 1, 16, idxs)
	}
	// checkErr bails to fail when the disc byte at retptr is nonzero.
	checkErr := func(body []byte) []byte {
		body = inst.InstLocalGet(body, retptr)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = fail(body)
		return inst.InstEnd(body)
	}

	var body []byte
	body = emitIpFlat(body, idxs, 0, 1, flat, tmp)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, retptr)

	body = inst.InstLocalGet(body, flat)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, createSock)
	body = inst.InstLocalGet(body, retptr)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, retptr, 4, 16, idxs)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, sock)

	body = inst.InstLocalGet(body, sock)
	body = inst.InstCall(body, netHandle)
	body = emitIpFlatWords(body, flat)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, startBind)
	body = checkErr(body)

	body = inst.InstLocalGet(body, sock)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, finishBind)
	body = checkErr(body)

	body = inst.InstLocalGet(body, sock)
	body = inst.InstLocalGet(body, 2)
	body = convert.InstI64ExtendI32U(body)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, setBacklog)
	body = checkErr(body)

	body = inst.InstLocalGet(body, sock)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, startListen)
	body = checkErr(body)

	body = inst.InstLocalGet(body, sock)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, finishListen)
	body = checkErr(body)

	// The 16-byte area becomes the owned record: (sock, 0, 0, 0).
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstLocalGet(body, sock)
	body = memory.InstI32Store(body, 2, 0)
	for _, off := range []uint32{4, 8, 12} {
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, off)
	}
	body = inst.InstLocalGet(body, retptr)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32), body)
}

// buildTcpSendfileBody assembles __fern_tcp_sendfile: (conn, file, max) →
// -ENOTSUP (58). wasi:sockets moves bytes through streams only, so the
// serve loop reads the file and sends the piece itself.
func buildTcpSendfileBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, -58)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildTcpSocketCtlBody assembles __fern_tcp_socket_ctl over a connection
// or listener record. op 2 (keep-alive), op 4 (shutdown), op 7 (the
// peer's address as a key, through remote-address), op 9 (the local
// address, through local-address) and op 10 (the peer's, through
// remote-address) are the controls wasi:sockets 0.2 has; op 1 (no-delay), op 3 (non-blocking) and op 6 (the send queue)
// have none and answer -ENOTSUP (58), so a caller learns the host owns
// Nagle, the blocking mode and the queue rather than believing it set or
// read them. Any other op is -EINVAL (28).
//
// Signature: (conn, op, arg: i32) → i32.
//
// Locals (after the three params): 3: $retptr (4 bytes; 36 for op 7),
// 4: $key (op 7's answer)
func buildTcpSocketCtlBody(idxs map[string]uint32) []byte {
	const retptr = 3
	const key = 4
	// The result<_, error-code> at retptr: 0, or -errno with the area freed.
	answer := func(body []byte) []byte {
		body = inst.InstLocalGet(body, retptr)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = emitErrnoNegReturnReclaim(body, retptr, 1, 4, idxs)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstI32Const(body, 4)
		body = inst.InstCall(body, idxs["__free"])
		body = inst.InstI32Const(body, 0)
		return inst.InstReturn(body)
	}
	opIs := func(body []byte, op int32) []byte {
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, op)
		body = numeric.InstI32Eq(body)
		return inst.InstIfStart(body, inst.BlocktypeEmpty)
	}

	var body []byte
	// op 1, 3, 6 and 8: no control on this target.
	for _, op := range []int32{1, 3, 6, 8} {
		body = opIs(body, op)
		body = inst.InstI32Const(body, -58)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
	}
	// op 7: the peer's address as a 32-bit key, the value the native
	// runtimes compute from the sockaddr (docs/BACKEND-PARITY.md): an
	// IPv4 address packed with its first octet in the low byte, a
	// v4-mapped IPv6 address its IPv4 one, any other IPv6 address the two
	// words of its first eight bytes XORed and never 0. 0 for a record
	// with no connected peer, a datagram record included, or an error.
	// remote-address answers `result<ip-socket-address, error-code>` in
	// the 36-byte area local-address uses: the family disc at +4, then
	// for ipv4 the port at +8 and four address bytes at +10, for ipv6 the
	// port at +8, flow-info at +12 and eight 16-bit groups at +16, each a
	// little-endian u16 of two network-order bytes.
	body = opIs(body, 7)
	{
		body = emitIsUdpRecord(body, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 0)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		body = inst.InstI32Const(body, 36)
		body = inst.InstCall(body, idxs["__fern_alloc"])
		body = inst.InstLocalSet(body, retptr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstCall(body, idxs["wasi_sockets_tcp_remote_address"])
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, key)
		// byte(off): one byte of the area.
		byteAt := func(body []byte, off uint32) []byte {
			body = inst.InstLocalGet(body, retptr)
			return memory.InstI32Load8U(body, 0, off)
		}
		// word(off): four address bytes as one word, the first in the
		// low byte; an ipv4 address lies in memory order, an ipv6 group's
		// two bytes are swapped by the u16 lowering.
		word := func(body []byte, off uint32, swapped bool) []byte {
			order := []uint32{0, 1, 2, 3}
			if swapped {
				order = []uint32{1, 0, 3, 2}
			}
			for n, o := range order {
				body = byteAt(body, off+o)
				if n > 0 {
					body = inst.InstI32Const(body, int32(8*n))
					body = numeric.InstI32Shl(body)
					body = numeric.InstI32Or(body)
				}
			}
			return body
		}
		body = byteAt(body, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = byteAt(body, 4)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				// ipv6: v4-mapped when the first ten bytes are 0 and the
				// next two 255, else the fold of the first eight.
				body = word(body, 16, true)
				body = word(body, 20, true)
				body = numeric.InstI32Or(body)
				body = inst.InstLocalGet(body, retptr)
				body = memory.InstI32Load16U(body, 1, 24)
				body = numeric.InstI32Or(body)
				body = numeric.InstI32Eqz(body)
				body = inst.InstLocalGet(body, retptr)
				body = memory.InstI32Load16U(body, 1, 26)
				body = inst.InstI32Const(body, 65535)
				body = numeric.InstI32Eq(body)
				body = numeric.InstI32And(body)
				body = inst.InstIfStart(body, inst.BlocktypeEmpty)
				body = word(body, 28, true)
				body = inst.InstLocalSet(body, key)
				body = inst.InstElse(body)
				body = word(body, 16, true)
				body = word(body, 20, true)
				body = numeric.InstI32Xor(body)
				body = inst.InstLocalSet(body, key)
				body = inst.InstLocalGet(body, key)
				body = numeric.InstI32Eqz(body)
				body = inst.InstIfStart(body, inst.BlocktypeEmpty)
				body = inst.InstI32Const(body, 1)
				body = inst.InstLocalSet(body, key)
				body = inst.InstEnd(body)
				body = inst.InstEnd(body)
			}
			body = inst.InstElse(body)
			body = word(body, 10, false)
			body = inst.InstLocalSet(body, key)
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstI32Const(body, 36)
		body = inst.InstCall(body, idxs["__free"])
		body = inst.InstLocalGet(body, key)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// op 9: the local address, through local-address (the udp method for
	// a datagram record), and op 10: the peer's, through remote-address,
	// each as 16-bit groups in the 36-byte area op 7 reads: arg 0..7 is
	// the group in network order (an ipv4 address fills 0 and 1 from its
	// four bytes at +10, the rest are 0; an ipv6 group is the u16 at +16 +
	// 2*arg), 8 the family as 4 or 6, 9 the port at +8; -EINVAL (28)
	// outside that range.
	nameGroups := func(body []byte, op int32, tcpMethod, udpMethod string) []byte {
		body = opIs(body, op)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 9)
		body = numeric.InstI32GtU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, -28)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		body = inst.InstI32Const(body, 36)
		body = inst.InstCall(body, idxs["__fern_alloc"])
		body = inst.InstLocalSet(body, retptr)
		query := func(body []byte, method uint32) []byte {
			body = inst.InstLocalGet(body, 0)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, retptr)
			return inst.InstCall(body, method)
		}
		if udpAddress, ok := idxs[udpMethod]; ok {
			body = emitIsUdpRecord(body, 0)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = query(body, udpAddress)
			body = inst.InstElse(body)
			body = query(body, idxs[tcpMethod])
			body = inst.InstEnd(body)
		} else {
			body = query(body, idxs[tcpMethod])
		}
		body = inst.InstLocalGet(body, retptr)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = emitErrnoNegReturnReclaim(body, retptr, 4, 36, idxs)
		body = inst.InstEnd(body)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, key)
		// retptr + 2*arg, the base the group loads add their offset to.
		groupAt := func(body []byte) []byte {
			body = inst.InstLocalGet(body, retptr)
			body = inst.InstLocalGet(body, 2)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Shl(body)
			return numeric.InstI32Add(body)
		}
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 9)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, retptr)
			body = memory.InstI32Load16U(body, 1, 8)
			body = inst.InstLocalSet(body, key)
		}
		body = inst.InstElse(body)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, retptr)
			body = memory.InstI32Load8U(body, 0, 4)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = inst.InstI32Const(body, 6)
			body = inst.InstLocalSet(body, key)
			body = inst.InstElse(body)
			body = inst.InstI32Const(body, 4)
			body = inst.InstLocalSet(body, key)
			body = inst.InstEnd(body)
		}
		body = inst.InstElse(body)
		{
			body = inst.InstLocalGet(body, retptr)
			body = memory.InstI32Load8U(body, 0, 4)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = groupAt(body)
			body = memory.InstI32Load16U(body, 1, 16)
			body = inst.InstLocalSet(body, key)
			body = inst.InstElse(body)
			body = inst.InstLocalGet(body, 2)
			body = inst.InstI32Const(body, 2)
			body = numeric.InstI32LtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = groupAt(body)
			body = memory.InstI32Load8U(body, 0, 10)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Shl(body)
			body = groupAt(body)
			body = memory.InstI32Load8U(body, 0, 11)
			body = numeric.InstI32Or(body)
			body = inst.InstLocalSet(body, key)
			body = inst.InstEnd(body)
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstI32Const(body, 36)
		body = inst.InstCall(body, idxs["__free"])
		body = inst.InstLocalGet(body, key)
		body = inst.InstReturn(body)
		return inst.InstEnd(body)
	}
	body = nameGroups(body, 9, "wasi_sockets_tcp_local_address", "wasi_sockets_udp_local_address")
	body = nameGroups(body, 10, "wasi_sockets_tcp_remote_address", "wasi_sockets_udp_remote_address")

	// A datagram record (wasi_udp.go) has neither control either.
	body = emitIsUdpRecord(body, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -58)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// op 5: the result of a connect. A connection is complete; a
	// connecting record runs finish-connect, which answers would-block
	// (code 8) while the connect is under way, reported as -EINPROGRESS
	// (26), and on Ok hands over the streams.
	body = opIs(body, 5)
	{
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 12)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 0)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 12)
		body = inst.InstI32Const(body, tcpRecordConnecting)
		body = numeric.InstI32Ne(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, -28)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, idxs["__fern_alloc"])
		body = inst.InstLocalSet(body, retptr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstCall(body, idxs["wasi_sockets_tcp_finish_connect"])
		body = inst.InstLocalGet(body, retptr)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, retptr)
		body = memory.InstI32Load8U(body, 0, 4)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, -26)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, idxs["__free"])
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		body = emitErrnoNegReturnReclaim(body, retptr, 4, 16, idxs)
		body = inst.InstEnd(body)
		for _, off := range []uint32{4, 8} {
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, retptr)
			body = memory.InstI32Load(body, 2, off)
			body = memory.InstI32Store(body, 2, off)
		}
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 12)
		body = inst.InstLocalGet(body, retptr)
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, idxs["__free"])
		body = inst.InstI32Const(body, 0)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstLocalSet(body, retptr)

	// op 2: set-keep-alive-enabled(sock, arg != 0, retptr).
	body = opIs(body, 2)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32Ne(body)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, idxs["wasi_sockets_tcp_set_keep_alive_enabled"])
	body = answer(body)
	body = inst.InstEnd(body)

	// op 4: shutdown(sock, arg, retptr).
	body = opIs(body, 4)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, retptr)
	body = inst.InstCall(body, idxs["wasi_sockets_tcp_shutdown"])
	body = answer(body)
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, retptr)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstI32Const(body, -28)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32), body)
}
