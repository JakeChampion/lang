package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

func buildWriterBytesBody(ids map[string]uint32) []byte     { return buildByteWrite(ids, true, false) }
func buildWriterSomeBytesBody(ids map[string]uint32) []byte { return buildByteWrite(ids, false, false) }
func buildWriterBytesBodyP2(ids map[string]uint32) []byte   { return buildByteWrite(ids, true, true) }
func buildWriterSomeBytesBodyP2(ids map[string]uint32) []byte {
	return buildByteWrite(ids, false, true)
}

// Params: Writer, borrowed packed array. Locals: scratch, handle, cursor,
// remaining, count, errno, error, result. Even an empty write checks the host.
func buildByteWrite(ids map[string]uint32, all, p2 bool) []byte {
	var b []byte
	b = inst.InstI32Const(b, writerScratchAddr)
	b = inst.InstLocalSet(b, 2)
	if p2 {
		b = inst.InstLocalGet(b, 0)
		b = memory.InstI64Load(b, 3, writerPosOff)
		b = inst.InstI64Const(b, -1)
		b = numeric.InstI64Eq(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = inst.InstI32Const(b, 8)
		b = inst.InstLocalSet(b, 7)
		b = emitByteWriteError(b, ids, all)
		b = inst.InstEnd(b)
	}
	b = inst.InstLocalGet(b, 0)
	b = memory.InstI32Load(b, 2, 0)
	b = inst.InstLocalSet(b, 3)
	b = inst.InstLocalGet(b, 1)
	b = inst.InstLocalSet(b, 4)
	b = inst.InstLocalGet(b, 1)
	b = inst.InstI32Const(b, 4)
	b = numeric.InstI32Sub(b)
	b = memory.InstI32Load(b, 2, 0)
	b = inst.InstLocalSet(b, 5)
	b = inst.InstLoopStart(b, inst.BlocktypeEmpty)
	b = inst.InstLocalGet(b, 5)
	b = inst.InstLocalSet(b, 6)
	if p2 {
		b = inst.InstLocalGet(b, 6)
		b = inst.InstI32Const(b, 4096)
		b = numeric.InstI32GtU(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = inst.InstI32Const(b, 4096)
		b = inst.InstLocalSet(b, 6)
		b = inst.InstEnd(b)
		for _, l := range []uint32{3, 4, 6, 2} {
			b = inst.InstLocalGet(b, l)
		}
		b = inst.InstCall(b, ids["wasi_blocking_write_and_flush_p2"])
		b = inst.InstLocalGet(b, 2)
		b = memory.InstI32Load8U(b, 0, 0)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = emitStreamErrorDrop(b, ids, 2)
		b = inst.InstI32Const(b, 29) // WASI EIO; stream errors carry no errno.
		b = inst.InstLocalSet(b, 7)
		b = emitByteWriteError(b, ids, all)
		b = inst.InstEnd(b)
		b = emitWriterAdvanceP2(b, 0, 6)
	} else {
		b = inst.InstLocalGet(b, 2)
		b = inst.InstLocalGet(b, 4)
		b = memory.InstI32Store(b, 2, 0)
		b = inst.InstLocalGet(b, 2)
		b = inst.InstLocalGet(b, 5)
		b = memory.InstI32Store(b, 2, 4)
		b = inst.InstLocalGet(b, 3)
		b = inst.InstLocalGet(b, 2)
		b = inst.InstI32Const(b, 1)
		b = inst.InstLocalGet(b, 2)
		b = inst.InstI32Const(b, 8)
		b = numeric.InstI32Add(b)
		b = inst.InstCall(b, ids["wasi_fd_write"])
		b = inst.InstLocalTee(b, 7)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = emitByteWriteError(b, ids, all)
		b = inst.InstEnd(b)
		b = inst.InstLocalGet(b, 2)
		b = memory.InstI32Load(b, 2, 8)
		b = inst.InstLocalSet(b, 6)
	}
	if all {
		b = inst.InstLocalGet(b, 6)
		b = inst.InstLocalGet(b, 5)
		b = numeric.InstI32Eq(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = emitPayloadlessResultBox(b, ids["__fern_alloc_rc1"], 9, 8, 1)
		b = inst.InstReturn(b)
		b = inst.InstEnd(b)
		b = inst.InstLocalGet(b, 6)
		b = numeric.InstI32Eqz(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = inst.InstI32Const(b, 29)
		b = inst.InstLocalSet(b, 7)
		b = emitByteWriteError(b, ids, all)
		b = inst.InstEnd(b)
		b = inst.InstLocalGet(b, 4)
		b = inst.InstLocalGet(b, 6)
		b = numeric.InstI32Add(b)
		b = inst.InstLocalSet(b, 4)
		b = inst.InstLocalGet(b, 5)
		b = inst.InstLocalGet(b, 6)
		b = numeric.InstI32Sub(b)
		b = inst.InstLocalSet(b, 5)
		b = inst.InstBr(b, 0)
	} else {
		b = emitHandleResultOkI64U(b, ids["__fern_alloc_rc1"], 6, 9)
		b = inst.InstReturn(b)
	}
	b = inst.InstEnd(b)
	b = inst.InstUnreachable(b)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32), b)
}

func emitByteWriteError(b []byte, ids map[string]uint32, all bool) []byte {
	if !all {
		return emitHandleResultErr(b, ids["__build_io_error"], ids["__fern_alloc_rc1"], 7, 8, 9)
	}
	b = inst.InstLocalGet(b, 7)
	b = inst.InstI32Const(b, 0)
	b = inst.InstI32Const(b, 0)
	b = inst.InstCall(b, ids["__build_io_error"])
	b = inst.InstLocalSet(b, 8)
	b = inst.InstI32Const(b, 8)
	b = inst.InstCall(b, ids["__fern_alloc_rc1"])
	b = inst.InstLocalTee(b, 9)
	b = inst.InstI32Const(b, 0)
	b = memory.InstI32Store(b, 2, 0)
	b = inst.InstLocalGet(b, 9)
	b = inst.InstLocalGet(b, 8)
	b = memory.InstI32Store(b, 2, 4)
	b = inst.InstLocalGet(b, 9)
	return inst.InstReturn(b)
}
