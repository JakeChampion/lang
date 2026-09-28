// UDP for the wasmbin backend: the datagram sockets of #9853 over
// wasi:sockets/udp.
//
// A datagram "fd" is the same 16-byte record the tcp helpers use
// (wasi_tcp.go): the udp-socket handle at +0, the incoming and outgoing
// datagram-stream handles at +4 and +8, and at +12 the kind word, 2 for a
// udp socket without streams and 3 for one with them (0 and 1 are the tcp
// listener and connection). tcp_close and tcp_local_port branch on that
// word, so one close and one port query serve every socket.
//
//	udp_bind      create-udp-socket → start-bind + finish-bind → stream(none)
//	udp_connect   drop the streams → stream(some(peer))
//	udp_sendto    check-send / subscribe / block until permitted → send
//	udp_recvfrom  receive(1), subscribing and blocking while nothing is pending
//	udp_close     drop the streams, then the socket
//	udp_send      udp_bind(0.0.0.0:0) → udp_sendto(host:port) → udp_close
//
// An ip-socket-address flattens to 13 i32 (the option discriminant, the
// family discriminant, the port, four octets and six padding slots); an
// outgoing-datagram record is 44 bytes (data ptr @0, len @4, the option
// disc @8, the family disc @12, the port @16 and the octets @18) and an
// incoming-datagram 40 (data ptr @0, len @4, the family disc @8, the port
// @12 and the octets @14).

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// errnoSocketInvalidArgument is EINVAL in the Preview 1 errno namespace used
// by socket return values, including errors produced before any host call.
const errnoSocketInvalidArgument = 28

// udpRecordBare and udpRecordStreams are the kind words of a datagram
// socket record without and with its streams.
const (
	udpRecordBare    = 2
	udpRecordStreams = 3
)

// emitOctetsFromPacked pushes the four octets of the IPv4 address packed
// in network order in local `packed`, first octet first, then six zero
// padding slots: the tail of an ipv4 ip-socket-address flattening.
func emitOctetsFromPacked(body []byte, packed uint32) []byte {
	for shift := int32(0); shift < 32; shift += 8 {
		body = inst.InstLocalGet(body, packed)
		body = inst.InstI32Const(body, shift)
		body = numeric.InstI32ShrU(body)
		body = inst.InstI32Const(body, 255)
		body = numeric.InstI32And(body)
	}
	for i := 0; i < 6; i++ {
		body = inst.InstI32Const(body, 0)
	}
	return body
}

// buildUdpSendBody assembles __fern_udp_send.
//
// Signature: (host_data, host_len, port, data_data, data_len) → i32 —
// the byte count accepted by the host, or -errno on a socket failure.
// String args lower to (ptr, len) pairs, so host + data arrive as two
// i32s each.
//
// `host` must be a dotted-quad IPv4 literal; anything else returns
// -EINVAL. The parse runs FIRST, before the socket exists, so a
// rejected host leaves nothing to drop. The socket itself is one bound
// to an ephemeral port, sent through and closed again.
//
// Locals (after the 5 params):
//
//	5:  $host_buf      SSO-normalized host pointer
//	6:  $host_blen     host byte length
//	7:  $i             normalize + parse loop index
//	8:  $octets        4-byte scratch holding the parsed ipv4 octets
//	9:  $octIdx        which octet (0..3) the parser is filling
//	10: $acc           current octet accumulator
//	11: $b             current host byte
//	12: $bad           host-parse rejection flag (0 = well-formed)
//	13: $digits        digits seen in the octet being parsed
//	14: $rec           the socket record from udp_bind
//	15: $sent          udp_sendto's answer
func buildUdpSendBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	free := func(body []byte, ptr uint32, size int32) []byte {
		body = inst.InstLocalGet(body, ptr)
		body = inst.InstI32Const(body, size)
		return inst.InstCall(body, idxs["__free"])
	}

	var body []byte

	// Parse the host as a dotted-quad IPv4 literal BEFORE creating the
	// socket, so a rejected host leaves no handle to drop. $bad
	// accumulates every rejection and is read once after the loop; the
	// octet store is guarded on $octIdx separately, because a host with
	// a fifth group would otherwise write past the 4-byte $octets.
	body = emitStrNormalize(body, idxs, 0, 1, 5, 6, 7)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 9) // octIdx
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 10) // acc
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // i
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 12) // bad
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 13) // digits
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// $b = mem[host_buf + i]
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstLocalSet(body, 11)
		// if $b == '.' (46): close the group, advance octIdx, reset acc
		body = inst.InstLocalGet(body, 11)
		body = inst.InstI32Const(body, 46)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// An empty group — a leading dot, or "1..2".
			body = inst.InstLocalGet(body, 13)
			body = numeric.InstI32Eqz(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalSet(body, 12)
			body = inst.InstEnd(body)
			// Store only while octIdx addresses one of the four bytes.
			body = inst.InstLocalGet(body, 9)
			body = inst.InstI32Const(body, 3)
			body = numeric.InstI32LtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				body = inst.InstLocalGet(body, 8)
				body = inst.InstLocalGet(body, 9)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalGet(body, 10)
				body = memory.InstI32Store8(body, 0, 0)
				body = inst.InstLocalGet(body, 9)
				body = inst.InstI32Const(body, 1)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalSet(body, 9)
			}
			body = inst.InstElse(body)
			{
				body = inst.InstI32Const(body, 1)
				body = inst.InstLocalSet(body, 12)
			}
			body = inst.InstEnd(body)
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 10)
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 13)
		}
		body = inst.InstElse(body)
		{
			// A byte outside '0'..'9'. Without this a hostname's
			// letters accumulated as (b - '0') and the socket
			// connected to whatever address they produced (#7740).
			body = inst.InstLocalGet(body, 11)
			body = inst.InstI32Const(body, 48)
			body = numeric.InstI32LtU(body)
			body = inst.InstLocalGet(body, 11)
			body = inst.InstI32Const(body, 57)
			body = numeric.InstI32GtU(body)
			body = numeric.InstI32Or(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalSet(body, 12)
			body = inst.InstEnd(body)
			// acc = acc*10 + (b - '0')
			body = inst.InstLocalGet(body, 10)
			body = inst.InstI32Const(body, 10)
			body = numeric.InstI32Mul(body)
			body = inst.InstLocalGet(body, 11)
			body = inst.InstI32Const(body, 48)
			body = numeric.InstI32Sub(body)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 10)
			body = inst.InstLocalGet(body, 13)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 13)
			// An octet past 255 doesn't fit the byte it addresses.
			// Per-digit, which also bounds acc: it is never above 255
			// entering a multiply, so acc*10+9 cannot wrap.
			body = inst.InstLocalGet(body, 10)
			body = inst.InstI32Const(body, 255)
			body = numeric.InstI32GtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalSet(body, 12)
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // loop
	body = inst.InstEnd(body) // block
	body = emitStrNormalizeFree(body, idxs, 1, 5, 6)

	// Reject unless four groups closed and the last carries a digit.
	body = inst.InstLocalGet(body, 12)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, 3)
	body = numeric.InstI32Ne(body)
	body = numeric.InstI32Or(body)
	body = inst.InstLocalGet(body, 13)
	body = numeric.InstI32Eqz(body)
	body = numeric.InstI32Or(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -errnoSocketInvalidArgument)
	body = free(body, 8, 4)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// store the final octet: $octets[3] = acc
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 9)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 10)
	body = memory.InstI32Store8(body, 0, 0)

	// $rec = udp_bind(0, 0); a negative answer is the -errno.
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, idxs["__fern_udp_bind"])
	body = inst.InstLocalTee(body, 14)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 14)
	body = free(body, 8, 4)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// $sent = udp_sendto($rec, packed octets, port, data); close; answer.
	body = inst.InstLocalGet(body, 14)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, idxs["__fern_udp_sendto"])
	body = inst.InstLocalSet(body, 15)
	body = inst.InstLocalGet(body, 14)
	body = inst.InstCall(body, idxs["__fern_udp_close"])
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 15)
	body = free(body, 8, 4)

	locals := inst.PutLocalsOneGroup(nil, 11, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildUdpBindBody assembles __fern_udp_bind.
//
// Signature: (host_be: i32, port: i32) → i32 — a socket record bound to
// the IPv4 address packed in network order, with its datagram streams
// open to any peer, or -errno. The 16-byte return area of the setup calls
// becomes the record: stream(none) lands the two stream handles at +4 and
// +8, where the record keeps them.
//
// Locals (after the two params):
//
//	2: $ret    16-byte return area, then the record
//	3: $sock   udp-socket handle
func buildUdpBindBody(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstLocalSet(body, 2)

	// create-udp-socket(ipv4, ret); the handle sits at +4 on Ok.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, idxs["wasi_sockets_create_udp_socket"])
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 2, 4, 16, idxs)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 3)

	fail := func(body []byte, errAt uint32) []byte {
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, idxs["wasi_sockets_udp_socket_drop"])
		return emitErrnoNegReturnReclaim(body, 2, errAt, 16, idxs)
	}

	// start-bind(sock, network, ipv4 host:port, ret) → finish-bind(sock, ret).
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, idxs["__network_handle"])
	body = inst.InstI32Const(body, 0) // family = ipv4
	body = inst.InstLocalGet(body, 1)
	body = emitOctetsFromPacked(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, idxs["wasi_sockets_udp_start_bind"])
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body, 1)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, idxs["wasi_sockets_udp_finish_bind"])
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body, 1)
	body = inst.InstEnd(body)

	// stream(sock, none, ret): the streams land at +4 and +8.
	body = inst.InstLocalGet(body, 3)
	for i := 0; i < 13; i++ {
		body = inst.InstI32Const(body, 0)
	}
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, idxs["wasi_sockets_udp_stream"])
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = fail(body, 4)
	body = inst.InstEnd(body)

	// The record: socket at +0, streams already at +4 / +8, kind at +12.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, udpRecordStreams)
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 2)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32), body)
}

// emitUdpDropStreams drops a datagram socket record's streams when it has
// them and marks the record bare.
func emitUdpDropStreams(body []byte, rec uint32, idxs map[string]uint32) []byte {
	body = inst.InstLocalGet(body, rec)
	body = memory.InstI32Load(body, 2, 12)
	body = inst.InstI32Const(body, udpRecordStreams)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, rec)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstCall(body, idxs["wasi_sockets_incoming_datagram_stream_drop"])
	body = inst.InstLocalGet(body, rec)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstCall(body, idxs["wasi_sockets_outgoing_datagram_stream_drop"])
	body = inst.InstLocalGet(body, rec)
	body = inst.InstI32Const(body, udpRecordBare)
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstEnd(body)
	return body
}

// buildUdpCloseBody assembles __fern_udp_close.
//
// Signature: (rec: i32) → i32 (always 0). Drops the streams the record
// holds, then the udp-socket, and releases the record. tcp_close hands a
// datagram record here.
func buildUdpCloseBody(idxs map[string]uint32) []byte {
	var body []byte
	body = emitUdpDropStreams(body, 0, idxs)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstCall(body, idxs["wasi_sockets_udp_socket_drop"])
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstI32Const(body, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 0, encode.ValtypeI32), body)
}

// buildUdpConnectBody assembles __fern_udp_connect.
//
// Signature: (rec: i32, host_be: i32, port: i32) → i32 — 0 once the
// record's streams are replaced by a pair fixed to the peer, or -errno with
// the record left bare: the host may trap on a stream() call while the old
// pair is alive, so they go first.
//
// Locals (after the three params):
//
//	3: $ret    16-byte return area
func buildUdpConnectBody(idxs map[string]uint32) []byte {
	var body []byte
	body = emitUdpDropStreams(body, 0, idxs)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstLocalSet(body, 3)

	// stream(sock, some(ipv4 host:port), ret).
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1) // option = some
	body = inst.InstI32Const(body, 0) // family = ipv4
	body = inst.InstLocalGet(body, 2)
	body = emitOctetsFromPacked(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, idxs["wasi_sockets_udp_stream"])
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = emitErrnoNegReturnReclaim(body, 3, 4, 16, idxs)
	body = inst.InstEnd(body)

	for _, off := range []uint32{4, 8} {
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load(body, 2, off)
		body = memory.InstI32Store(body, 2, off)
	}
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, udpRecordStreams)
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstI32Const(body, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32), body)
}

// buildUdpSendtoBody assembles __fern_udp_sendto.
//
// Signature: (rec, host_be, port, data_data, data_len) → i32 — the byte
// count once the host accepts the datagram, or -errno. A zero host and
// port address the connected peer (remote-address none); otherwise the
// record carries some(ipv4 host:port).
//
// Two would-block shapes hide in the send: check-send reports a permit of
// 0 until the socket is writable, and wasmtime ≥45 rejects a send that
// exceeds the permit, so the permit wait blocks on the stream's pollable;
// and send may accept fewer datagrams than offered even after a positive
// permit, so an accepted count of 0 re-enters the wait.
//
// Locals (after the five params):
//
//	5:  $ret    16-byte return area (the u64 counts land at +8)
//	6:  $out    outgoing-datagram-stream handle
//	7:  $buf    SSO-normalized data pointer
//	8:  $blen   data byte length
//	9:  $i      normalize scratch
//	10: $dg     the one-element list<outgoing-datagram>
//	11: $sent   the answer
//	12: $poll   pollable handle for the permit wait
func buildUdpSendtoBody(idxs map[string]uint32) []byte {
	var body []byte
	body = emitStrNormalize(body, idxs, 3, 4, 7, 8, 9)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstLocalSet(body, 5)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstLocalSet(body, 6)

	// The datagram record: data, then the optional address.
	body = inst.InstI32Const(body, 44)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstLocalSet(body, 10)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstLocalGet(body, 7)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 4)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Or(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 10)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 8)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 12)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Store16(body, 1, 16)
		for i := uint32(0); i < 4; i++ {
			body = inst.InstLocalGet(body, 10)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, int32(8*i))
			body = numeric.InstI32ShrU(body)
			body = memory.InstI32Store8(body, 0, 18+i)
		}
	}
	body = inst.InstElse(body)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 8)
	body = inst.InstEnd(body)

	// $sent = -errno from the result at $ret, then leave the send loop.
	failed := func(body []byte, errAt uint32, depth uint32) []byte {
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load8U(body, 0, errAt)
		body = inst.InstCall(body, idxs["__fern_wasi_socket_errno"])
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 11)
		return inst.InstBr(body, depth)
	}

	body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // done
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)  // send again
	{
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // permitted
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)  // wait
		{
			body = inst.InstLocalGet(body, 6)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstCall(body, idxs["wasi_sockets_udp_check_send"])
			body = inst.InstLocalGet(body, 5)
			body = memory.InstI32Load8U(body, 0, 0)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			body = failed(body, 8, 4)
			body = inst.InstEnd(body)
			body = inst.InstLocalGet(body, 5)
			body = memory.InstI32Load(body, 2, 8)
			body = inst.InstBrIf(body, 1)
			body = inst.InstLocalGet(body, 6)
			body = inst.InstCall(body, idxs["wasi_sockets_udp_outgoing_subscribe"])
			body = inst.InstLocalTee(body, 12)
			body = inst.InstCall(body, idxs["wasi_io_pollable_block"])
			body = inst.InstLocalGet(body, 12)
			body = inst.InstCall(body, idxs["wasi_io_pollable_drop"])
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)

		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstI32Const(body, 1)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstCall(body, idxs["wasi_sockets_udp_send"])
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = failed(body, 8, 2)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load(body, 2, 8)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 0)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalSet(body, 11)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, 44)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, idxs["__free"])
	body = emitStrNormalizeFree(body, idxs, 4, 7, 8)
	body = inst.InstLocalGet(body, 11)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32), body)
}

// buildUdpRecvfromBody assembles __fern_udp_recvfrom.
//
// Signature: (rec, buf, from) → i32 — one datagram copied into the u8[]
// at buf, up to the length at buf-4: its byte count, or -errno. receive
// answers an empty list while nothing is pending, so the call subscribes
// and blocks on the incoming stream and asks again. When the u8[] at
// `from` holds six bytes or more and the sender is IPv4, its octets and
// then its port, high byte first, are written there.
//
// The list and each datagram's bytes were placed in this heap by
// cabi_realloc, so both are released once copied.
//
// Locals (after the three params):
//
//	3: $ret    12-byte return area: list ptr @4, count @8
//	4: $in     incoming-datagram-stream handle
//	5: $list   the one-element list<incoming-datagram>
//	6: $n      bytes copied, then the answer
//	7: $dptr   the datagram's bytes
//	8: $dlen   their count
//	9: $poll   pollable handle for the wait
func buildUdpRecvfromBody(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstLocalSet(body, 3)

	// The length word of a u8[] sits four bytes before its data.
	arrayLen := func(body []byte, arr uint32) []byte {
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, -4)
		body = numeric.InstI32Add(body)
		return memory.InstI32Load(body, 2, 0)
	}

	body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // done
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)  // again
	{
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI64Const(body, 1)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, idxs["wasi_sockets_udp_receive"])
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load8U(body, 0, 4)
		body = inst.InstCall(body, idxs["__fern_wasi_socket_errno"])
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 2)
		body = inst.InstEnd(body)
		// Nothing pending: wait for the stream, then ask again.
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load(body, 2, 8)
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, idxs["wasi_sockets_udp_incoming_subscribe"])
		body = inst.InstLocalTee(body, 9)
		body = inst.InstCall(body, idxs["wasi_io_pollable_block"])
		body = inst.InstLocalGet(body, 9)
		body = inst.InstCall(body, idxs["wasi_io_pollable_drop"])
		body = inst.InstBr(body, 1)
		body = inst.InstEnd(body)

		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, 5)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, 8)
		// $n = min(dlen, len(buf)); copy that much.
		body = inst.InstLocalGet(body, 8)
		body = arrayLen(body, 1)
		body = inst.InstLocalGet(body, 8)
		body = arrayLen(body, 1)
		body = numeric.InstI32LtU(body)
		body = inst.InstSelect(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 6)
		body = memory.InstMemoryCopy(body)
		// The sender, when there is room for it and it is IPv4.
		body = arrayLen(body, 2)
		body = inst.InstI32Const(body, 6)
		body = numeric.InstI32GeU(body)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load8U(body, 0, 8)
		body = numeric.InstI32Eqz(body)
		body = numeric.InstI32And(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		for i := uint32(0); i < 4; i++ {
			body = inst.InstLocalGet(body, 2)
			body = inst.InstLocalGet(body, 5)
			body = memory.InstI32Load8U(body, 0, 14+i)
			body = memory.InstI32Store8(body, 0, i)
		}
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load8U(body, 0, 13)
		body = memory.InstI32Store8(body, 0, 4)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 5)
		body = memory.InstI32Load8U(body, 0, 12)
		body = memory.InstI32Store8(body, 0, 5)
		body = inst.InstEnd(body)
		// Release the host's list and the datagram's bytes.
		body = inst.InstLocalGet(body, 8)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstCall(body, idxs["__free"])
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 40)
		body = inst.InstCall(body, idxs["__free"])
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, idxs["__free"])
	body = inst.InstLocalGet(body, 6)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32), body)
}

// Only inline strings own the buffer created by emitStrNormalize. Heap-form
// strings are borrowed from the caller and must never enter the freelist.
func emitStrNormalizeFree(body []byte, idxs map[string]uint32, lenLocal, bufLocal, byteLenLocal uint32) []byte {
	body = inst.InstLocalGet(body, lenLocal)
	body = inst.InstI32Const(body, -0x80000000)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, bufLocal)
	body = inst.InstLocalGet(body, byteLenLocal)
	body = inst.InstCall(body, idxs["__free"])
	return inst.InstEnd(body)
}
