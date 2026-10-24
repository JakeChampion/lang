package inst

import (
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

const BlocktypeEmpty byte = 0x40

func PutBlocktypeTypeidx(buf []byte, idx int32) []byte {
	return leb128.SlebI32(buf, idx)
}

func InstI32Const(buf []byte, v int32) []byte {
	return leb128.SlebI32(append(buf, 0x41), v)
}

func InstI64Const(buf []byte, v int64) []byte {
	return leb128.SlebI64(append(buf, 0x42), v)
}

func InstF32Const(buf []byte, bits uint32) []byte {
	return append(buf, 0x43,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
}

func InstF64Const(buf []byte, bits uint64) []byte {
	return append(buf, 0x44,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
		byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56))
}

func InstDrop(buf []byte) []byte   { return append(buf, 0x1a) }
func InstSelect(buf []byte) []byte { return append(buf, 0x1b) }

func InstLocalGet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x20), idx)
}
func InstLocalSet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x21), idx)
}
func InstLocalTee(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x22), idx)
}
func InstGlobalGet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x23), idx)
}
func InstGlobalSet(buf []byte, idx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x24), idx)
}

func InstUnreachable(buf []byte) []byte { return append(buf, 0x00) }
func InstNop(buf []byte) []byte         { return append(buf, 0x01) }

func InstBlockStart(buf []byte, blocktype byte) []byte {
	return append(buf, 0x02, blocktype)
}
func InstLoopStart(buf []byte, blocktype byte) []byte {
	return append(buf, 0x03, blocktype)
}
func InstIfStart(buf []byte, blocktype byte) []byte {
	return append(buf, 0x04, blocktype)
}
func InstElse(buf []byte) []byte   { return append(buf, 0x05) }
func InstEnd(buf []byte) []byte    { return append(buf, 0x0b) }
func InstReturn(buf []byte) []byte { return append(buf, 0x0f) }

func InstBr(buf []byte, labelidx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x0c), labelidx)
}
func InstBrIf(buf []byte, labelidx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x0d), labelidx)
}
func InstCall(buf []byte, funcidx uint32) []byte {
	return leb128.UlebU32(append(buf, 0x10), funcidx)
}

func InstCallIndirect(buf []byte, typeidx, tableidx uint32) []byte {
	buf = leb128.UlebU32(append(buf, 0x11), typeidx)
	return leb128.UlebU32(buf, tableidx)
}

func PutLocalsEmpty(buf []byte) []byte {
	return leb128.UlebU32(buf, 0)
}

func PutLocalsOneGroup(buf []byte, count uint32, vt byte) []byte {
	buf = leb128.UlebU32(buf, 1)
	buf = leb128.UlebU32(buf, count)
	return append(buf, vt)
}

func PutFunctionBody(buf, localsBytes, bodyBytes []byte) []byte {
	inner := make([]byte, 0, len(localsBytes)+len(bodyBytes)+1)
	inner = append(inner, localsBytes...)
	inner = append(inner, bodyBytes...)
	inner = append(inner, 0x0b)
	buf = leb128.UlebU32(buf, uint32(len(inner)))
	return append(buf, inner...)
}
