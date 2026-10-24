package imports

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

const (
	ImportFunc   byte = 0
	ImportTable  byte = 1
	ImportMemory byte = 2
	ImportGlobal byte = 3
)

const (
	MutConst byte = 0
	MutVar   byte = 1
)

const (
	ReftypeFuncref   byte = 0x70
	ReftypeExternref byte = 0x6f
)

func ImportDescFunc(typeidx uint32) []byte {
	return leb128.UlebU32(nil, typeidx)
}

func ImportDescGlobal(vt, isMut byte) []byte {
	return []byte{vt, isMut}
}

func ImportDescMemory(minPages uint32, maxPages int32) []byte {
	if maxPages < 0 {
		return leb128.UlebU32([]byte{0x00}, minPages)
	}
	out := leb128.UlebU32([]byte{0x01}, minPages)
	return leb128.UlebU32(out, uint32(maxPages))
}

func ImportDescTable(reftype byte, min uint32, max int32) []byte {
	if max < 0 {
		return leb128.UlebU32([]byte{reftype, 0x00}, min)
	}
	out := leb128.UlebU32([]byte{reftype, 0x01}, min)
	return leb128.UlebU32(out, uint32(max))
}

func EncodeImportSection(buf []byte, modules, names []string, kinds []byte, descBodies [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(modules)))
	for i := range modules {
		body = encode.PutName(body, modules[i])
		body = encode.PutName(body, names[i])
		body = append(body, kinds[i])
		body = append(body, descBodies[i]...)
	}
	return encode.PutSection(buf, encode.SectionImport, body)
}

func EncodeGlobalSection(buf []byte, valtypes, muts []byte, initExprs [][]byte) []byte {
	body := leb128.UlebU32(nil, uint32(len(valtypes)))
	for i := range valtypes {
		body = append(body, valtypes[i], muts[i])
		body = append(body, initExprs[i]...)
	}
	return encode.PutSection(buf, encode.SectionGlobal, body)
}
