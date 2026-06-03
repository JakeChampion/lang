// UDP send for the wasmbin backend. `udp_send(host, port, data)` is a
// one-shot fire-and-forget datagram (send-only / IPv4-literal v1, for
// telemetry / syslog to a local agent):
//
//	create-udp-socket(ipv4) → start-bind + finish-bind(0.0.0.0:0) →
//	stream(Some(host:port))  [connect] → wait for a send permit
//	(check-send / subscribe / pollable.block loop) → send([{data}]) →
//	drop the incoming + outgoing datagram streams, then the socket.
//
// Connecting via stream(Some(remote)) puts the destination address in
// the 15-i32 flattened param (the same ip-socket-address flattening
// tcp start-bind uses), so the outgoing-datagram carries
// remote-address: none — a 60-byte record whose only set fields are
// data (ptr@+0, len@+4) and the option discriminant (0 @ +8). That
// avoids hand-marshalling the ip-socket-address variant into the
// datagram.
//
// Composition (compose_udp.go) lowers the method set + the three
// resource drops; the imports come from scanImports's __fern_udp_send
// branch.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// buildUdpSendBody assembles __fern_udp_send.
//
// Signature: (host_data, host_len, port, data_data, data_len) → i32 —
// the byte count accepted by the host (== data length when the single
// datagram is sent), or -errno on a socket failure. String args lower
// to (ptr, len) pairs, so host + data arrive as two i32s each.
//
// Locals (after the 5 params):
//
//	5:  $retptr        16-byte canonical-ABI return scratch
//	6:  $sock          udp-socket handle
//	7:  $outStream     outgoing-datagram-stream handle
//	8:  $inStream      incoming-datagram-stream handle (dropped unused)
//	9:  $data_buf      SSO-normalized data pointer
//	10: $data_blen     data byte length
//	11: $i             scratch (normalize + parse loop index)
//	12: $host_buf      SSO-normalized host pointer
//	13: $host_blen     host byte length
//	14: $octets        4-byte scratch holding the parsed ipv4 octets
//	15: $octIdx        which octet (0..3) the parser is filling
//	16: $acc           current octet accumulator
//	17: $b             current host byte
//	18: $dgram         60+-byte outgoing-datagram record
//	19: $sent          datagrams accepted (low 32 bits of the u64)
//	20: $poll          pollable handle (send-permit wait loop)
func buildUdpSendBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	netHandle := idxs["__network_handle"]
	createSock := idxs["wasi_sockets_create_udp_socket"]
	startBind := idxs["wasi_sockets_udp_start_bind"]
	finishBind := idxs["wasi_sockets_udp_finish_bind"]
	stream := idxs["wasi_sockets_udp_stream"]
	checkSend := idxs["wasi_sockets_udp_check_send"]
	subscribe := idxs["wasi_sockets_udp_outgoing_subscribe"]
	pollBlock := idxs["wasi_io_pollable_block"]
	pollDrop := idxs["wasi_io_pollable_drop"]
	send := idxs["wasi_sockets_udp_send"]
	sockDrop := idxs["wasi_sockets_udp_socket_drop"]
	inDrop := idxs["wasi_sockets_incoming_datagram_stream_drop"]
	outDrop := idxs["wasi_sockets_outgoing_datagram_stream_drop"]

	var body []byte

	// retptr = alloc(16) — check-send / send land result<u64,…> with
	// the u64 at +8, so 16 bytes.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 5)

	// create-udp-socket(ipv4=0, retptr); bail on Err.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, createSock)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturn(body, 5)
	body = inst.InstEnd(body)
	// $sock = mem[retptr+4]
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 6)

	// start-bind(sock, network, ipv4 0.0.0.0:0, retptr) — 15 i32.
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, netHandle)
	body = inst.InstI32Const(body, 0) // disc = ipv4
	body = inst.InstI32Const(body, 0) // port = 0 (ephemeral)
	body = inst.InstI32Const(body, 0) // ipv4 byte 0
	body = inst.InstI32Const(body, 0) // ipv4 byte 1
	body = inst.InstI32Const(body, 0) // ipv4 byte 2
	body = inst.InstI32Const(body, 0) // ipv4 byte 3
	for i := 0; i < 6; i++ {
		body = inst.InstI32Const(body, 0) // 6 padding slots
	}
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, startBind)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturn(body, 5)
	body = inst.InstEnd(body)

	// finish-bind(sock, retptr); bail on Err.
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, finishBind)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturn(body, 5)
	body = inst.InstEnd(body)

	// Normalize host into a contiguous buffer, then parse the dotted
	// quad into 4 octet bytes at $octets.
	body = emitStrNormalize(body, idxs, 0, 1, 12, 13, 11)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 14)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 15) // octIdx
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 16) // acc
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11) // i
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 13)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// $b = mem[host_buf + i]
		body = inst.InstLocalGet(body, 12)
		body = inst.InstLocalGet(body, 11)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstLocalSet(body, 17)
		// if $b == '.' (46): store acc, advance octIdx, reset acc
		body = inst.InstLocalGet(body, 17)
		body = inst.InstI32Const(body, 46)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 14)
			body = inst.InstLocalGet(body, 15)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 16)
			body = memory.InstI32Store8(body, 0, 0)
			body = inst.InstLocalGet(body, 15)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 15)
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 16)
		}
		body = inst.InstElse(body)
		{
			// acc = acc*10 + (b - '0')
			body = inst.InstLocalGet(body, 16)
			body = inst.InstI32Const(body, 10)
			body = numeric.InstI32Mul(body)
			body = inst.InstLocalGet(body, 17)
			body = inst.InstI32Const(body, 48)
			body = numeric.InstI32Sub(body)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 16)
		}
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 11)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 11)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // loop
	body = inst.InstEnd(body) // block
	// store the final octet: $octets[octIdx] = acc
	body = inst.InstLocalGet(body, 14)
	body = inst.InstLocalGet(body, 15)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 16)
	body = memory.InstI32Store8(body, 0, 0)

	// stream(sock, Some(ipv4 host:port), retptr) — connect. The option
	// flattens to opt_disc=1, ipaddr_disc=0(ipv4), port, 4 octets, 6
	// padding = 13 i32; + self + retptr = 15.
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 1) // option disc = some
	body = inst.InstI32Const(body, 0) // ip-socket-address disc = ipv4
	body = inst.InstLocalGet(body, 2) // port
	for i := 0; i < 4; i++ {
		body = inst.InstLocalGet(body, 14)
		body = inst.InstI32Const(body, int32(i))
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
	}
	for i := 0; i < 6; i++ {
		body = inst.InstI32Const(body, 0) // 6 padding slots
	}
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, stream)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturn(body, 5)
	body = inst.InstEnd(body)
	// $inStream = mem[retptr+4], $outStream = mem[retptr+8]
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstLocalSet(body, 7)

	// Normalize the payload into a contiguous buffer.
	body = emitStrNormalize(body, idxs, 3, 4, 9, 10, 11)

	// Wait until the outgoing-datagram-stream permits at least one
	// datagram. check-send returns result<u64, error-code> — the u64
	// permit count at retptr+8. Right after connect wasmtime can report
	// a permit of 0 until the socket is writable; sending then trips
	// "unpermitted: argument exceeds permitted size" (wasmtime ≥45). So
	// loop: check-send → if permit ≥1 break, else block on the stream's
	// pollable and retry.
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // break target (br 1)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)  // retry target (br 0)
	{
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstCall(body, checkSend)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = emitErrnoNegReturn(body, 5)
		body = inst.InstEnd(body)
		// permit (low 32 of the u64 @ +8): if non-zero, break the loop.
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load(body, 2, 8)
		body = inst.InstBrIf(body, 1)
		// permit == 0: subscribe → block → drop, then retry.
		body = inst.InstLocalGet(body, 7)
		body = inst.InstCall(body, subscribe)
		body = inst.InstLocalSet(body, 20)
		body = inst.InstLocalGet(body, 20)
		body = inst.InstCall(body, pollBlock)
		body = inst.InstLocalGet(body, 20)
		body = inst.InstCall(body, pollDrop)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // loop
	body = inst.InstEnd(body) // block

	// Build the 1-element list<outgoing-datagram>. Each record is 60
	// bytes; alloc 64. remote-address: none → only data (ptr@+0,
	// len@+4) and the option disc (0 @ +8) are set.
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 18)
	body = inst.InstLocalGet(body, 18)
	body = inst.InstLocalGet(body, 9)
	body = memory.InstI32Store(body, 2, 0) // data ptr @ +0
	body = inst.InstLocalGet(body, 18)
	body = inst.InstLocalGet(body, 10)
	body = memory.InstI32Store(body, 2, 4) // data len @ +4
	body = inst.InstLocalGet(body, 18)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 8) // option disc = none @ +8

	// send(outStream, list_ptr=$dgram, list_len=1, retptr); bail on Err.
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 18)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, send)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturn(body, 5)
	body = inst.InstEnd(body)
	// $sent = low 32 bits of the u64 datagram count at retptr+8.
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstLocalSet(body, 19)

	// Drop the datagram streams (children) before the udp-socket.
	body = inst.InstLocalGet(body, 8)
	body = inst.InstCall(body, inDrop)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, outDrop)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, sockDrop)

	// Return the data length when ≥1 datagram was accepted, else 0.
	body = inst.InstLocalGet(body, 19)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstElse(body)
	body = inst.InstI32Const(body, 0)
	body = inst.InstEnd(body)

	locals := inst.PutLocalsOneGroup(nil, 16, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
