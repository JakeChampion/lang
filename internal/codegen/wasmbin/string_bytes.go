package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// Copy (string data, string length) into a fresh owned u8 array. The three
// locals hold the decoded length, result pointer and inline byte index.
func buildStringBytesCopyBody(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, idxs["__fern_str_len"])
	body = inst.InstLocalTee(body, 2)
	body = inst.InstCall(body, idxs["__alloc_u8"])
	body = inst.InstLocalSet(body, 3)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, -0x80000000)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32GeS(body)
	body = inst.InstBrIf(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, idxs["__fern_str_byte"])
	body = memory.InstI32Store8(body, 0, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstBr(body, 0)
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstElse(body)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstMemoryCopy(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 3)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32), body)
}
