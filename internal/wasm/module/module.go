// Package module is the Go-side mirror of
// internal/stdlib/std/wasm/module.lang — top-level module
// composer for the WebAssembly Core 1.0 binary format.
//
// Spec: https://webassembly.github.io/spec/core/binary/modules.html
//
// Bundles internal/wasm/{encode, sections, imports} behind a
// single Build entry point: a caller fills in a Module struct
// field-by-field, then calls Build(m) to get the complete
// module bytes back.
//
// Section ordering matters — the wasm spec requires sections
// in the order: type, import, function, table, memory, global,
// export, start, element, code, data. Build emits them in
// that order, skipping any section whose input is empty.
package module

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/imports"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// Module bundles per-section inputs each section composer takes.
// Vector sections (type/import/function/global/export/code/data)
// skip emitting their header when empty. Singleton sections
// (memory, start) gate on the matching boolean.
type Module struct {
	// Type section
	TypeParams  [][]byte
	TypeResults [][]byte

	// Import section
	ImportModules []string
	ImportNames   []string
	ImportKinds   []byte
	ImportDescs   [][]byte

	// Function section — one typeidx per declared (non-imported)
	// function, in declaration order.
	FunctionTypeidxs []uint32

	// Memory section. wasm 1.0 caps memories at 1, so a single
	// optional limits record is enough.
	MemoryPresent bool
	MemoryMin     uint32
	MemoryMax     int32

	// Table section. wasm 1.0 caps tables at 1; reftype is
	// fixed to funcref (the only reftype in MVP). Same gating
	// pattern as memory.
	TablePresent bool
	TableMin     uint32
	TableMax     int32

	// Element section — one active segment per index. The
	// caller supplies the per-segment offset (i32.const <off>)
	// and funcidx vector. wasm requires the matching table
	// (always table 0) to be wide enough to hold every slot.
	ElementOffsets  []int32
	ElementFuncidxs [][]uint32

	// Global section
	GlobalValtypes []byte
	GlobalMuts     []byte
	GlobalInits    [][]byte

	// Export section
	ExportNames []string
	ExportKinds []byte
	ExportIdxs  []uint32

	// Start section
	HasStart     bool
	StartFuncidx uint32

	// Code section — one pre-wrapped function body
	// (from inst.PutFunctionBody) per typeidx in FunctionTypeidxs.
	CodeBodies [][]byte

	// Data section
	DataOffsets []int32
	DataInits   [][]byte
}

// New returns a Module with every section empty. Callers
// populate the fields they need before calling Build.
func New() Module {
	return Module{
		MemoryMax: -1,
		TableMax:  -1,
	}
}

// Build serialises the module to the complete wasm binary:
// preamble (\0asm + version 1) followed by every populated
// section in spec-required order.
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
