package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// The socket builtins take an address as a u8[] of four or sixteen
// network-order bytes (#9853). wasi:sockets takes an ip-socket-address,
// which flattens to a family discriminant (0 ipv4, 1 ipv6) and eleven
// payload i32: for ipv4 the port, the four octets and six zero pads; for
// ipv6 the port, a zero flow label, the eight 16-bit segments and a zero
// scope id. __fern_ip_flat writes that flattening to a 48-byte record
// once, and each bind, connect and stream pushes its twelve words from
// there.
//
// The record and the two boxes below live in the module's reserved low
// memory rather than the heap: a socket body consumes them before it
// returns and none holds one across a call into another socket body, and
// an allocation here would show as one more `__heap_alloc_count()` tick
// per call, which the guest storage probes pin at exactly one.

// errnoSocketAddressFamily is EAFNOSUPPORT in the Preview 1 errno namespace
// used by socket return values.
const errnoSocketAddressFamily = 5

// ipFlatSize is the byte size of the record __fern_ip_flat writes.
const ipFlatSize = 48

// ipFlatAddr is the record's fixed address (wasmbin.go's memory map).
const ipFlatAddr = 96

// ipBoxAddr and ipBox2Addr are two fixed 8-byte `u8[]` boxes, a length
// word of 4 then four octets, for the bodies that still take a packed
// IPv4 address (tcp_connect, udp_send) and bind to 0.0.0.0.
const (
	ipBoxAddr  = 144
	ipBox2Addr = 152
)

// emitIpBox sets local `box` to a fixed box's DATA pointer, with the
// length word written and the octets set from local `packed`, or zero for
// a negative `packed`.
func emitIpBox(body []byte, box uint32, addr int32, packed int32) []byte {
	body = inst.InstI32Const(body, addr)
	body = inst.InstI32Const(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstI32Const(body, addr)
	if packed < 0 {
		body = inst.InstI32Const(body, 0)
	} else {
		body = inst.InstLocalGet(body, uint32(packed))
	}
	body = memory.InstI32Store(body, 2, 4)
	body = inst.InstI32Const(body, addr+4)
	return inst.InstLocalSet(body, box)
}

// buildIpFlatBody assembles __fern_ip_flat.
//
// Signature: (addr, port, out) → i32 — 0 with the flattening of addr:port
// at out, or -EAFNOSUPPORT for an address of another length, with out
// zeroed.
//
// Locals (after the three params):
//
//	3: $len   the u8[]'s length, from the word before its bytes
func buildIpFlatBody(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, -4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)

	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, ipFlatSize)
	body = memory.InstMemoryFill(body)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Store(body, 2, 4)

	// ipv4: family 0, the octets in slots 2..5.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	for k := uint32(0); k < 4; k++ {
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load8U(body, 0, k)
		body = memory.InstI32Store(body, 2, 8+4*k)
	}
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// ipv6: family 1, the segments in slots 3..10.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	for k := uint32(0); k < 8; k++ {
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load8U(body, 0, 2*k)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Shl(body)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load8U(body, 0, 2*k+1)
		body = numeric.InstI32Or(body)
		body = memory.InstI32Store(body, 2, 12+4*k)
	}
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, -errnoSocketAddressFamily)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32), body)
}

// emitIpFlat points local `flat` at the record and fills it from the
// address in local `addr` and the port in local `port`, with `tmp` holding
// the helper's answer; a refused length returns its -errno.
func emitIpFlat(body []byte, idxs map[string]uint32, addr, port, flat, tmp uint32) []byte {
	body = inst.InstI32Const(body, ipFlatAddr)
	body = inst.InstLocalSet(body, flat)
	body = inst.InstLocalGet(body, addr)
	body = inst.InstLocalGet(body, port)
	body = inst.InstLocalGet(body, flat)
	body = inst.InstCall(body, idxs["__fern_ip_flat"])
	body = inst.InstLocalTee(body, tmp)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, tmp)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	return body
}

// emitArrayLen pushes the length of the u8[] in local `arr`: the word four
// bytes before its data.
func emitArrayLen(body []byte, arr uint32) []byte {
	body = inst.InstLocalGet(body, arr)
	body = inst.InstI32Const(body, -4)
	body = numeric.InstI32Add(body)
	return memory.InstI32Load(body, 2, 0)
}

// emitIpFlatWords pushes the record's twelve words: the family
// discriminant and the eleven payload slots.
func emitIpFlatWords(body []byte, flat uint32) []byte {
	for k := uint32(0); k < 12; k++ {
		body = inst.InstLocalGet(body, flat)
		body = memory.InstI32Load(body, 2, 4*k)
	}
	return body
}
