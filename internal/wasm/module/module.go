package module

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/imports"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

type Module struct {
	TypeParams  [][]byte
	TypeResults [][]byte

	ImportModules []string
	ImportNames   []string
	ImportKinds   []byte
	ImportDescs   [][]byte

	FunctionTypeidxs []uint32

	MemoryPresent bool
	MemoryMin     uint32
	MemoryMax     int32

	TablePresent bool
	TableMin     uint32
	TableMax     int32

	ElementOffsets  []int32
	ElementFuncidxs [][]uint32

	GlobalValtypes []byte
	GlobalMuts     []byte
	GlobalInits    [][]byte

	ExportNames []string
	ExportKinds []byte
	ExportIdxs  []uint32

	HasStart     bool
	StartFuncidx uint32

	CodeBodies [][]byte

	DataOffsets []int32
	DataInits   [][]byte
}

func New() Module {
	return Module{
		MemoryMax: -1,
		TableMax:  -1,
	}
}

func Build(m Module) []byte {
	bytes := encode.PutModuleHeader(nil)

	if len(m.TypeParams) > 0 {
		bytes = sections.EncodeTypeSection(bytes, m.TypeParams, m.TypeResults)
	}

	if len(m.ImportModules) > 0 {
		bytes = imports.EncodeImportSection(bytes, m.ImportModules, m.ImportNames, m.ImportKinds, m.ImportDescs)
	}

	if len(m.FunctionTypeidxs) > 0 {
		bytes = sections.EncodeFunctionSection(bytes, m.FunctionTypeidxs)
	}

	if m.TablePresent {
		bytes = sections.EncodeTableSection(bytes, m.TableMin, m.TableMax)
	}

	if m.MemoryPresent {
		bytes = sections.EncodeMemorySection(bytes, m.MemoryMin, m.MemoryMax)
	}

	if len(m.GlobalValtypes) > 0 {
		bytes = imports.EncodeGlobalSection(bytes, m.GlobalValtypes, m.GlobalMuts, m.GlobalInits)
	}

	if len(m.ExportNames) > 0 {
		bytes = sections.EncodeExportSection(bytes, m.ExportNames, m.ExportKinds, m.ExportIdxs)
	}

	if m.HasStart {
		bytes = sections.EncodeStartSection(bytes, m.StartFuncidx)
	}

	if len(m.ElementOffsets) > 0 {
		bytes = sections.EncodeElementSection(bytes, m.ElementOffsets, m.ElementFuncidxs)
	}

	if len(m.CodeBodies) > 0 {
		bytes = sections.EncodeCodeSection(bytes, m.CodeBodies)
	}

	if len(m.DataOffsets) > 0 {
		bytes = sections.EncodeDataSection(bytes, m.DataOffsets, m.DataInits)
	}

	return bytes
}
