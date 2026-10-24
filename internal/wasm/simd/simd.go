package simd

import "github.com/jakechampion/lang/internal/wasm/leb128"

const Prefix byte = 0xFD

func put(buf []byte, sub uint32) []byte {
	return leb128.UlebU32(append(buf, Prefix), sub)
}

func putMem(buf []byte, sub, align, offset uint32) []byte {
	buf = put(buf, sub)
	buf = leb128.UlebU32(buf, align)
	return leb128.UlebU32(buf, offset)
}

func InstV128Load(buf []byte, align, offset uint32) []byte {
	return putMem(buf, 0, align, offset)
}

func InstV128Store(buf []byte, align, offset uint32) []byte {
	return putMem(buf, 11, align, offset)
}

func InstI8x16Splat(buf []byte) []byte { return put(buf, 15) }
func InstI16x8Splat(buf []byte) []byte { return put(buf, 16) }
func InstI32x4Splat(buf []byte) []byte { return put(buf, 17) }
func InstI64x2Splat(buf []byte) []byte { return put(buf, 18) }

func InstF64x2Splat(buf []byte) []byte { return put(buf, 20) }

func InstF64x2Mul(buf []byte) []byte { return put(buf, 242) }

func InstI8x16Eq(buf []byte) []byte  { return put(buf, 35) }
func InstI8x16Ne(buf []byte) []byte  { return put(buf, 36) }
func InstI8x16LtU(buf []byte) []byte { return put(buf, 38) }
func InstI8x16GtU(buf []byte) []byte { return put(buf, 40) }
func InstI8x16LeU(buf []byte) []byte { return put(buf, 42) }
func InstI8x16GeU(buf []byte) []byte { return put(buf, 44) }

func InstI16x8Eq(buf []byte) []byte { return put(buf, 45) }
func InstI16x8Ne(buf []byte) []byte { return put(buf, 46) }
func InstI32x4Eq(buf []byte) []byte { return put(buf, 55) }
func InstI32x4Ne(buf []byte) []byte { return put(buf, 56) }
func InstI64x2Eq(buf []byte) []byte { return put(buf, 214) }
func InstI64x2Ne(buf []byte) []byte { return put(buf, 215) }

func InstV128Not(buf []byte) []byte    { return put(buf, 77) }
func InstV128And(buf []byte) []byte    { return put(buf, 78) }
func InstV128AndNot(buf []byte) []byte { return put(buf, 79) }
func InstV128Or(buf []byte) []byte     { return put(buf, 80) }
func InstV128Xor(buf []byte) []byte    { return put(buf, 81) }

func InstV128AnyTrue(buf []byte) []byte { return put(buf, 83) }

func InstI8x16AllTrue(buf []byte) []byte { return put(buf, 99) }
func InstI8x16Bitmask(buf []byte) []byte { return put(buf, 100) }
func InstI16x8AllTrue(buf []byte) []byte { return put(buf, 131) }
func InstI16x8Bitmask(buf []byte) []byte { return put(buf, 132) }
func InstI32x4AllTrue(buf []byte) []byte { return put(buf, 163) }
func InstI32x4Bitmask(buf []byte) []byte { return put(buf, 164) }
func InstI64x2AllTrue(buf []byte) []byte { return put(buf, 195) }
func InstI64x2Bitmask(buf []byte) []byte { return put(buf, 196) }
