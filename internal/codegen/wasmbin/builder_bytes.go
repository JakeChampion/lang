package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// (handle) -> owned u8[]; locals hold the length and independent array.
func buildBufTakeBytesBody(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 1)
	pushN := func(b []byte) []byte { return inst.InstLocalGet(b, 1) }
	// Same size guard and owned header as __alloc_u8, with the payload fully
	// initialized by memory.copy instead of zero fill followed by copy.
	body = pushN(body)
	body = inst.InstI32Const(body, maxAllocRequest-arrHeaderBytes)
	body = numeric.InstI32GtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	body = emitArrHeaderAlloc(body, idxs["__fern_alloc"], 2, arrRcOwned, pushN, pushN)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstMemoryCopy(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 4)
	body = inst.InstLocalGet(body, 2)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32), body)
}
