package memory

import "github.com/jakechampion/lang/internal/wasm/leb128"

func putMemarg(buf []byte, align, offset uint32) []byte {
	buf = leb128.UlebU32(buf, align)
	return leb128.UlebU32(buf, offset)
}

func InstI32Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x28), align, offset)
}
func InstI64Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x29), align, offset)
}
func InstF32Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2a), align, offset)
}
func InstF64Load(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2b), align, offset)
}
func InstI32Load8S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2c), align, offset)
}
func InstI32Load8U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2d), align, offset)
}
func InstI32Load16S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2e), align, offset)
}
func InstI32Load16U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x2f), align, offset)
}
func InstI64Load8S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x30), align, offset)
}
func InstI64Load8U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x31), align, offset)
}
func InstI64Load16S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x32), align, offset)
}
func InstI64Load16U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x33), align, offset)
}
func InstI64Load32S(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x34), align, offset)
}
func InstI64Load32U(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x35), align, offset)
}

func InstI32Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x36), align, offset)
}
func InstI64Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x37), align, offset)
}
func InstF32Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x38), align, offset)
}
func InstF64Store(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x39), align, offset)
}
func InstI32Store8(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3a), align, offset)
}
func InstI32Store16(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3b), align, offset)
}
func InstI64Store8(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3c), align, offset)
}
func InstI64Store16(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3d), align, offset)
}
func InstI64Store32(buf []byte, align, offset uint32) []byte {
	return putMemarg(append(buf, 0x3e), align, offset)
}

func InstMemorySize(buf []byte) []byte { return append(buf, 0x3f, 0x00) }
func InstMemoryGrow(buf []byte) []byte { return append(buf, 0x40, 0x00) }

func InstMemoryCopy(buf []byte) []byte { return append(buf, 0xFC, 0x0A, 0x00, 0x00) }
func InstMemoryFill(buf []byte) []byte { return append(buf, 0xFC, 0x0B, 0x00) }
