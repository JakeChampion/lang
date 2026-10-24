package sections

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

const (
	ExportFunc   byte = 0
	ExportTable  byte = 1
	ExportMemory byte = 2
	ExportGlobal byte = 3
)

func EncodeCustomSection(buf []byte, name string, payload []byte) []byte {
	body := encode.PutName(nil, name)
	body = append(body, payload...)
	return encode.PutSection(buf, encode.SectionCustom, body)
}

func EncodeTypeSection(buf []byte, paramsPerType, resultsPerType [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(paramsPerType)))
	for i := range paramsPerType {
		body = encode.PutFuncType(body, paramsPerType[i], resultsPerType[i])
	}
	return encode.PutSection(buf, encode.SectionType, body)
}

func EncodeFunctionSection(buf []byte, typeidxs []uint32) []byte {
	body := leb128.UlebU32(nil, uint32(len(typeidxs)))
	for _, t := range typeidxs {
		body = leb128.UlebU32(body, t)
	}
	return encode.PutSection(buf, encode.SectionFunction, body)
}

func EncodeMemorySection(buf []byte, minPages uint32, maxPages int32) []byte {
	body := leb128.UlebU32(nil, 1)
	if maxPages < 0 {
		body = append(body, 0x00)
		body = leb128.UlebU32(body, minPages)
	} else {
		body = append(body, 0x01)
		body = leb128.UlebU32(body, minPages)
		body = leb128.UlebU32(body, uint32(maxPages))
	}
	return encode.PutSection(buf, encode.SectionMemory, body)
}

func EncodeGlobalSection(buf []byte, valtypes, muts []byte, initExprs [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(valtypes)))
	for i := range valtypes {
		body = append(body, valtypes[i], muts[i])
		body = append(body, initExprs[i]...)
	}
	return encode.PutSection(buf, encode.SectionGlobal, body)
}

func EncodeExportSection(buf []byte, names []string, kinds []byte, indices []uint32) []byte {
	body := leb128.UlebU32(nil, uint32(len(names)))
	for i := range names {
		body = encode.PutName(body, names[i])
		body = append(body, kinds[i])
		body = leb128.UlebU32(body, indices[i])
	}
	return encode.PutSection(buf, encode.SectionExport, body)
}

func EncodeStartSection(buf []byte, funcidx uint32) []byte {
	body := leb128.UlebU32(nil, funcidx)
	return encode.PutSection(buf, encode.SectionStart, body)
}

func EncodeTableSection(buf []byte, minSlots uint32, maxSlots int32) []byte {
	body := leb128.UlebU32(nil, 1)
	body = append(body, 0x70)
	if maxSlots < 0 {
		body = append(body, 0x00)
		body = leb128.UlebU32(body, minSlots)
	} else {
		body = append(body, 0x01)
		body = leb128.UlebU32(body, minSlots)
		body = leb128.UlebU32(body, uint32(maxSlots))
	}
	return encode.PutSection(buf, encode.SectionTable, body)
}

func EncodeElementSection(buf []byte, offsets []int32, funcidxs [][]uint32) []byte {
	body := leb128.UlebU32(nil, uint32(len(offsets)))
	for i, off := range offsets {
		body = append(body, 0x00)
		body = append(body, 0x41)
		body = leb128.SlebI32(body, off)
		body = append(body, 0x0b)
		body = leb128.UlebU32(body, uint32(len(funcidxs[i])))
		for _, idx := range funcidxs[i] {
			body = leb128.UlebU32(body, idx)
		}
	}
	return encode.PutSection(buf, encode.SectionElement, body)
}

func EncodeCodeSection(buf []byte, bodies [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(bodies)))
	for _, b := range bodies {
		body = append(body, b...)
	}
	return encode.PutSection(buf, encode.SectionCode, body)
}

func EncodeDataSection(buf []byte, offsets []int32, initBytes [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(offsets)))
	for i, off := range offsets {
		body = leb128.UlebU32(body, 0)
		body = append(body, 0x41)
		body = leb128.SlebI32(body, off)
		body = append(body, 0x0b)
		seg := initBytes[i]
		body = leb128.UlebU32(body, uint32(len(seg)))
		body = append(body, seg...)
	}
	return encode.PutSection(buf, encode.SectionData, body)
}
