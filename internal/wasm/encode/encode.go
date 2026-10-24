package encode

import "github.com/jakechampion/lang/internal/wasm/leb128"

const (
	ValtypeI32 byte = 0x7f
	ValtypeI64 byte = 0x7e
	ValtypeF32 byte = 0x7d
	ValtypeF64 byte = 0x7c
)

const (
	SectionCustom   byte = 0
	SectionType     byte = 1
	SectionImport   byte = 2
	SectionFunction byte = 3
	SectionTable    byte = 4
	SectionMemory   byte = 5
	SectionGlobal   byte = 6
	SectionExport   byte = 7
	SectionStart    byte = 8
	SectionElement  byte = 9
	SectionCode     byte = 10
	SectionData     byte = 11
)

func PutBytes(buf, src []byte) []byte {
	return append(buf, src...)
}

func PutU32LE(buf []byte, v uint32) []byte {
	return append(buf,
		byte(v),
		byte(v>>8),
		byte(v>>16),
		byte(v>>24))
}

func PutModuleHeader(buf []byte) []byte {
	buf = append(buf, 0x00, 'a', 's', 'm')
	return PutU32LE(buf, 1)
}

func PutName(buf []byte, s string) []byte {
	buf = leb128.UlebU32(buf, uint32(len(s)))
	return append(buf, s...)
}

func PutSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = leb128.UlebU32(buf, uint32(len(body)))
	return append(buf, body...)
}

func PutFuncType(buf, params, results []byte) []byte {
	buf = append(buf, 0x60)
	buf = leb128.UlebU32(buf, uint32(len(params)))
	buf = append(buf, params...)
	buf = leb128.UlebU32(buf, uint32(len(results)))
	return append(buf, results...)
}
