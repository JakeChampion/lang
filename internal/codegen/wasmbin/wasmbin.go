// Package wasmbin is a binary WebAssembly emitter that consumes the
// shared ir.Program and produces a core module's bytes directly,
// without going through WAT text. Sits next to internal/codegen/wasm
// (the WAT path) during the cutover — both share the lowering and IR
// optimisation pipeline, so a feature added to ir.Lower lights up
// here automatically once the corresponding op handler is wired.
//
// The aim is for this package to fully replace the WAT emitter and
// the `wasm-tools parse` shell-out it depends on; each release lands
// another slice of op coverage until every program the WAT path
// compiles also compiles through this one.
//
// Slice 1 scope (this file): integer + float constants, integer +
// float arithmetic / comparison / logical-not, locals (param +
// declared + tee), drop, explicit return + return-void. Enough for
// pure-arithmetic functions over single-slot scalar types. Strings,
// control flow, calls, memory, allocator, closures, pairs, and the
// preview-2 component wrapper are out of scope and return an
// `unsupported op` error.
package wasmbin

import (
	"fmt"
	"math"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/langstring"
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/module"
	"github.com/jakechampion/lang/internal/wasm/numeric"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// Emit produces a wasm core module's bytes for the given IR program.
// Every function in prog.Funcs is added to the module and exported
// under its IR name (so `wasmtime run --invoke <name>` can reach it).
// Function order is preserved — callers downstream rely on funcidx
// being assigned in declaration order.
//
// Returns an error if any function uses an op or operand type the
// current slice doesn't support.
func Emit(prog *ir.Program) ([]byte, error) {
	m := module.New()

	// Build a name → funcidx map so OpCallDirect can resolve
	// its callee. Funcidx is the declaration index — which
	// matches FunctionTypeidxs / CodeBodies / ExportIdxs since
	// every function is also exported by name. Imports would
	// shift this offset; the binary path doesn't emit imports
	// yet (the WASI / preview-2 wiring lives in a later slice).
	funcIdx := make(map[string]uint32, len(prog.Funcs))
	for i, fn := range prog.Funcs {
		funcIdx[fn.Name] = uint32(i)
	}

	// Type-section dedup: same param-list + result-list → same
	// typeidx. The string key joins valtype bytes; collisions
	// are impossible since valtype bytes are in 0x7c..0x7f.
	typeIdx := map[string]uint32{}
	addType := func(params, results []byte) uint32 {
		key := string(params) + "|" + string(results)
		if idx, ok := typeIdx[key]; ok {
			return idx
		}
		idx := uint32(len(m.TypeParams))
		typeIdx[key] = idx
		m.TypeParams = append(m.TypeParams, params)
		m.TypeResults = append(m.TypeResults, results)
		return idx
	}

	// String interning state for OpConstStr's heap-form path.
	// Inline-form strings (≤7 bytes via langstring.FitsInlineWasm)
	// pack into the two i32.consts directly and don't visit the
	// data section. Heap-form strings get a unique offset; the
	// data section's bytes are accumulated here in declaration
	// order, with the per-entry offset stored alongside the bytes.
	// stringStart matches the WAT path's choice of 1024 so the
	// data segment doesn't collide with the low-memory pair-cells
	// the closures slice will later allocate (the WAT path uses
	// the same convention).
	stringPool := map[string]int{}
	const stringStart = 1024
	stringNextOff := stringStart
	var dataBytes []byte
	internString := func(s string) int {
		if off, ok := stringPool[s]; ok {
			return off
		}
		off := stringNextOff
		stringPool[s] = off
		dataBytes = append(dataBytes, s...)
		stringNextOff = off + len(s)
		return off
	}

	ctx := &emitCtx{
		funcIdx:       funcIdx,
		internString:  internString,
		addSigType: func(sig *ast.FuncType) (uint32, error) {
			params := make([]byte, 0, len(sig.Params))
			for _, pt := range sig.Params {
				vt, err := valtypeFor(pt)
				if err != nil {
					return 0, err
				}
				params = append(params, vt)
			}
			results, err := resultValtypes(sig.Result)
			if err != nil {
				return 0, err
			}
			return addType(params, results), nil
		},
	}

	// Table section is emitted iff the program contains any
	// indirect-call op (OpCallIndirect / OpCallClosureDirect /
	// OpConstFunc). Slice 6 includes every function in the
	// program in the funcref table at its declaration index —
	// the simplest layout that lets OpCallIndirect dispatch by
	// funcidx.
	if anyTableOp(prog) {
		n := uint32(len(prog.Funcs))
		m.TablePresent = true
		m.TableMin = n
		m.TableMax = -1
		idxs := make([]uint32, n)
		for i := range idxs {
			idxs[i] = uint32(i)
		}
		m.ElementOffsets = []int32{0}
		m.ElementFuncidxs = [][]uint32{idxs}
		// Export the table too — useful for hosts that want to
		// poke at the slot layout. Same canonical name the WAT
		// emitter uses.
		m.ExportNames = append(m.ExportNames, "__indirect_function_table")
		m.ExportKinds = append(m.ExportKinds, sections.ExportTable)
		m.ExportIdxs = append(m.ExportIdxs, 0)
	}

	// Memory section is emitted iff any function in the program
	// touches memory (load / store / sub-width variants / fN load
	// or store). Slice-4 modules ship a single linear memory of
	// 1 page (64 KiB) with no upper bound — same shape the WAT
	// emitter produces for non-string programs.
	if anyMemoryOp(prog) {
		m.MemoryPresent = true
		m.MemoryMin = 1
		m.MemoryMax = -1
		// Export the memory under the canonical name so tests
		// and host tooling can poke at it.
		m.ExportNames = append(m.ExportNames, "memory")
		m.ExportKinds = append(m.ExportKinds, sections.ExportMemory)
		m.ExportIdxs = append(m.ExportIdxs, 0)
	}

	for fnIdx, fn := range prog.Funcs {
		params, err := paramValtypes(fn.Params)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
		}
		results, err := resultValtypes(fn.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
		}

		tIdx := addType(params, results)
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, tIdx)
		m.ExportNames = append(m.ExportNames, fn.Name)
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		m.ExportIdxs = append(m.ExportIdxs, uint32(fnIdx))

		body, locals, err := emitBody(fn, ctx)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
		}
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
	}

	// Heap-form strings → data segment. Even if no other op used
	// memory, the data segment requires a memory; force one in
	// that case. The single segment lives at stringStart (1024)
	// matching the WAT path so subsequent heap allocations land
	// after the literals.
	if len(dataBytes) > 0 {
		if !m.MemoryPresent {
			m.MemoryPresent = true
			m.MemoryMin = 1
			m.MemoryMax = -1
			m.ExportNames = append(m.ExportNames, "memory")
			m.ExportKinds = append(m.ExportKinds, sections.ExportMemory)
			m.ExportIdxs = append(m.ExportIdxs, 0)
		}
		m.DataOffsets = []int32{int32(stringStart)}
		m.DataInits = [][]byte{dataBytes}
	}

	return module.Build(m), nil
}

// valtypeFor maps a single ast.Type to the wasm valtype byte used to
// hold it. Only single-slot scalar types are supported in slice 1;
// strings (two-slot ABI) and compound heap-pointer types come in
// later slices.
func valtypeFor(t ast.Type) (byte, error) {
	switch v := t.(type) {
	case ast.NumberType:
		if v.NormalWidth() == 64 {
			return encode.ValtypeI64, nil
		}
		return encode.ValtypeI32, nil
	case ast.BoolType:
		return encode.ValtypeI32, nil
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return encode.ValtypeF64, nil
		}
		return encode.ValtypeF32, nil
	}
	return 0, fmt.Errorf("unsupported type %s (slice 1 covers scalar i32/i64/f32/f64 + bool only)", t)
}

// paramValtypes returns the wasm param valtype vector for an IR
// function's parameter list. Each param maps to exactly one wasm
// slot in slice 1 (no strings).
func paramValtypes(params []ast.Param) ([]byte, error) {
	out := make([]byte, 0, len(params))
	for _, p := range params {
		vt, err := valtypeFor(p.Type)
		if err != nil {
			return nil, err
		}
		out = append(out, vt)
	}
	return out, nil
}

// resultValtypes returns the wasm result valtype vector for an IR
// function's return type. Void → empty; scalar → one slot.
func resultValtypes(t ast.Type) ([]byte, error) {
	if t == nil {
		return nil, nil
	}
	if _, isVoid := t.(ast.VoidType); isVoid {
		return nil, nil
	}
	vt, err := valtypeFor(t)
	if err != nil {
		return nil, err
	}
	return []byte{vt}, nil
}

// localValtypes returns the wasm valtype vector for an IR function's
// declared locals + scratch slots — exactly what the local-section
// preamble of the function body needs.
func localValtypes(fn *ir.Func) ([]byte, error) {
	out := make([]byte, 0, len(fn.Locals)+len(fn.ScratchTypes))
	for _, l := range fn.Locals {
		vt, err := valtypeFor(l.Type)
		if err != nil {
			return nil, err
		}
		out = append(out, vt)
	}
	for _, s := range fn.ScratchTypes {
		vt, err := valtypeFor(s)
		if err != nil {
			return nil, err
		}
		out = append(out, vt)
	}
	return out, nil
}

// emitCtx bundles per-program lookups shared across every op
// emitted in this build. Growing this struct is preferable to
// growing emitOp's signature for each new slice.
type emitCtx struct {
	// funcIdx maps an IR function name to its funcidx in the
	// emitted module. OpCallDirect / OpCallClosureDirect use it.
	funcIdx map[string]uint32
	// addSigType resolves a function-type signature to its
	// typeidx, lazily inserting into the type section. Used by
	// OpCallIndirect, whose op.Sig carries the static signature.
	addSigType func(*ast.FuncType) (uint32, error)
	// internString returns the data-segment offset for the
	// heap-form bytes of s, interning so repeats share an
	// address. Used by OpConstStr.
	internString func(string) int
}

// emitBody walks fn.Ops and returns the function's body bytes plus
// its locals-preamble bytes (the latter pre-wrapped by
// inst.PutLocalsOneGroup-equivalent encoding for the declared local
// valtypes).
func emitBody(fn *ir.Func, ctx *emitCtx) (body, locals []byte, err error) {
	lvts, err := localValtypes(fn)
	if err != nil {
		return nil, nil, err
	}
	locals = encodeLocals(lvts)

	for opIdx, op := range fn.Ops {
		body, err = emitOp(body, op, ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("op[%d] %v: %w", opIdx, op.Kind, err)
		}
	}
	return body, locals, nil
}

// encodeLocals emits the run-length-encoded local-vec for a
// function body: groups consecutive identical valtypes into a single
// (count, valtype) record. Empty → the "no locals" encoding.
func encodeLocals(vts []byte) []byte {
	if len(vts) == 0 {
		return inst.PutLocalsEmpty(nil)
	}
	var out []byte
	// vec length prefix is the number of runs; we don't know it
	// ahead of time, so build the run-list first then prepend.
	var runs [][2]uint32 // {count, valtype}
	i := 0
	for i < len(vts) {
		j := i + 1
		for j < len(vts) && vts[j] == vts[i] {
			j++
		}
		runs = append(runs, [2]uint32{uint32(j - i), uint32(vts[i])})
		i = j
	}
	// vec(locals)
	out = appendUleb(out, uint32(len(runs)))
	for _, r := range runs {
		out = appendUleb(out, r[0])
		out = append(out, byte(r[1]))
	}
	return out
}

// emitOp translates one IR op into its wasm bytes and appends them
// to body. Op coverage grows slice-by-slice; unsupported ops
// return an error rather than emitting invalid bytes.
func emitOp(body []byte, op ir.Op, ctx *emitCtx) ([]byte, error) {
	switch op.Kind {
	case ir.OpConstI32:
		return inst.InstI32Const(body, op.I32), nil
	case ir.OpConstI64:
		return inst.InstI64Const(body, op.I64), nil
	case ir.OpConstF32:
		return inst.InstF32Const(body, math.Float32bits(op.F32)), nil
	case ir.OpConstF64:
		return inst.InstF64Const(body, math.Float64bits(op.F64)), nil

	case ir.OpConstStr:
		// Two-word string ABI: every OpConstStr pushes `(data, len)`
		// onto the operand stack as two i32 values.
		// Inline-form (≤7 bytes, ASCII-only via FitsInlineWasm)
		// packs into the two i32.consts directly and doesn't touch
		// the data section. Heap-form interns into the data segment
		// and emits (data_offset, length).
		if langstring.FitsInlineWasm(len(op.Str)) {
			data, length := langstring.PackInlineWasm([]byte(op.Str))
			body = inst.InstI32Const(body, int32(data))
			body = inst.InstI32Const(body, int32(length))
			return body, nil
		}
		off := ctx.internString(op.Str)
		body = inst.InstI32Const(body, int32(off))
		body = inst.InstI32Const(body, int32(len(op.Str)))
		return body, nil

	case ir.OpLoadLocal:
		return inst.InstLocalGet(body, uint32(op.I32)), nil
	case ir.OpStoreLocal:
		return inst.InstLocalSet(body, uint32(op.I32)), nil
	case ir.OpTeeLocal:
		return inst.InstLocalTee(body, uint32(op.I32)), nil

	case ir.OpDrop:
		return inst.InstDrop(body), nil
	case ir.OpReturn:
		return inst.InstReturn(body), nil
	case ir.OpReturnVoid:
		return inst.InstReturn(body), nil

	case ir.OpBlock:
		bt, err := blocktypeByte(op.I32)
		if err != nil {
			return nil, err
		}
		return inst.InstBlockStart(body, bt), nil
	case ir.OpLoop:
		bt, err := blocktypeByte(op.I32)
		if err != nil {
			return nil, err
		}
		return inst.InstLoopStart(body, bt), nil
	case ir.OpIf:
		bt, err := blocktypeByte(op.I32)
		if err != nil {
			return nil, err
		}
		return inst.InstIfStart(body, bt), nil
	case ir.OpElse:
		return inst.InstElse(body), nil
	case ir.OpEnd:
		return inst.InstEnd(body), nil
	case ir.OpBr:
		return inst.InstBr(body, uint32(op.I32)), nil
	case ir.OpBrIf:
		return inst.InstBrIf(body, uint32(op.I32)), nil

	case ir.OpAdd:
		if op.Width == 64 {
			return numeric.InstI64Add(body), nil
		}
		return numeric.InstI32Add(body), nil
	case ir.OpSub:
		if op.Width == 64 {
			return numeric.InstI64Sub(body), nil
		}
		return numeric.InstI32Sub(body), nil
	case ir.OpMul:
		if op.Width == 64 {
			return numeric.InstI64Mul(body), nil
		}
		return numeric.InstI32Mul(body), nil
	case ir.OpDivS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64DivU(body), nil
			}
			return numeric.InstI64DivS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32DivU(body), nil
		}
		return numeric.InstI32DivS(body), nil
	case ir.OpRemS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64RemU(body), nil
			}
			return numeric.InstI64RemS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32RemU(body), nil
		}
		return numeric.InstI32RemS(body), nil
	case ir.OpAnd:
		if op.Width == 64 {
			return numeric.InstI64And(body), nil
		}
		return numeric.InstI32And(body), nil
	case ir.OpOr:
		if op.Width == 64 {
			return numeric.InstI64Or(body), nil
		}
		return numeric.InstI32Or(body), nil
	case ir.OpXor:
		if op.Width == 64 {
			return numeric.InstI64Xor(body), nil
		}
		return numeric.InstI32Xor(body), nil
	case ir.OpShl:
		if op.Width == 64 {
			return numeric.InstI64Shl(body), nil
		}
		return numeric.InstI32Shl(body), nil
	case ir.OpShrS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64ShrU(body), nil
			}
			return numeric.InstI64ShrS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32ShrU(body), nil
		}
		return numeric.InstI32ShrS(body), nil
	case ir.OpNot:
		// logical not — i32.eqz; only meaningful on i32.
		return numeric.InstI32Eqz(body), nil

	case ir.OpEq:
		if op.Width == 64 {
			return numeric.InstI64Eq(body), nil
		}
		return numeric.InstI32Eq(body), nil
	case ir.OpNe:
		if op.Width == 64 {
			return numeric.InstI64Ne(body), nil
		}
		return numeric.InstI32Ne(body), nil
	case ir.OpLtS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64LtU(body), nil
			}
			return numeric.InstI64LtS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32LtU(body), nil
		}
		return numeric.InstI32LtS(body), nil
	case ir.OpLeS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64LeU(body), nil
			}
			return numeric.InstI64LeS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32LeU(body), nil
		}
		return numeric.InstI32LeS(body), nil
	case ir.OpGtS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64GtU(body), nil
			}
			return numeric.InstI64GtS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32GtU(body), nil
		}
		return numeric.InstI32GtS(body), nil
	case ir.OpGeS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64GeU(body), nil
			}
			return numeric.InstI64GeS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32GeU(body), nil
		}
		return numeric.InstI32GeS(body), nil

	case ir.OpFAdd:
		if op.Width == 64 {
			return numeric.InstF64Add(body), nil
		}
		return numeric.InstF32Add(body), nil
	case ir.OpFSub:
		if op.Width == 64 {
			return numeric.InstF64Sub(body), nil
		}
		return numeric.InstF32Sub(body), nil
	case ir.OpFMul:
		if op.Width == 64 {
			return numeric.InstF64Mul(body), nil
		}
		return numeric.InstF32Mul(body), nil
	case ir.OpFDiv:
		if op.Width == 64 {
			return numeric.InstF64Div(body), nil
		}
		return numeric.InstF32Div(body), nil
	case ir.OpFNeg:
		if op.Width == 64 {
			return numeric.InstF64Neg(body), nil
		}
		return numeric.InstF32Neg(body), nil
	case ir.OpFEq:
		if op.Width == 64 {
			return numeric.InstF64Eq(body), nil
		}
		return numeric.InstF32Eq(body), nil
	case ir.OpFNe:
		if op.Width == 64 {
			return numeric.InstF64Ne(body), nil
		}
		return numeric.InstF32Ne(body), nil
	case ir.OpFLt:
		if op.Width == 64 {
			return numeric.InstF64Lt(body), nil
		}
		return numeric.InstF32Lt(body), nil
	case ir.OpFLe:
		if op.Width == 64 {
			return numeric.InstF64Le(body), nil
		}
		return numeric.InstF32Le(body), nil
	case ir.OpFGt:
		if op.Width == 64 {
			return numeric.InstF64Gt(body), nil
		}
		return numeric.InstF32Gt(body), nil
	case ir.OpFGe:
		if op.Width == 64 {
			return numeric.InstF64Ge(body), nil
		}
		return numeric.InstF32Ge(body), nil

	// ---- Conversions (slice 3) ----
	case ir.OpExtendI32S:
		return convert.InstI64ExtendI32S(body), nil
	case ir.OpExtendI32U:
		return convert.InstI64ExtendI32U(body), nil
	case ir.OpWrapI64:
		return convert.InstI32WrapI64(body), nil
	case ir.OpFPromoteF32:
		return convert.InstF64PromoteF32(body), nil
	case ir.OpFDemoteF64:
		return convert.InstF32DemoteF64(body), nil
	case ir.OpSignExtend8:
		return convert.InstI32Extend8S(body), nil
	case ir.OpSignExtend16:
		return convert.InstI32Extend16S(body), nil
	case ir.OpReinterpretI32F32:
		return convert.InstI32ReinterpretF32(body), nil
	case ir.OpReinterpretF32I32:
		return convert.InstF32ReinterpretI32(body), nil

	case ir.OpFConvertI32:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstF64ConvertI32U(body), nil
			}
			return convert.InstF64ConvertI32S(body), nil
		}
		if op.Unsigned {
			return convert.InstF32ConvertI32U(body), nil
		}
		return convert.InstF32ConvertI32S(body), nil
	case ir.OpFConvertI64:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstF64ConvertI64U(body), nil
			}
			return convert.InstF64ConvertI64S(body), nil
		}
		if op.Unsigned {
			return convert.InstF32ConvertI64U(body), nil
		}
		return convert.InstF32ConvertI64S(body), nil
	case ir.OpITruncF32:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstI64TruncF32U(body), nil
			}
			return convert.InstI64TruncF32S(body), nil
		}
		if op.Unsigned {
			return convert.InstI32TruncF32U(body), nil
		}
		return convert.InstI32TruncF32S(body), nil
	case ir.OpITruncF64:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstI64TruncF64U(body), nil
			}
			return convert.InstI64TruncF64S(body), nil
		}
		if op.Unsigned {
			return convert.InstI32TruncF64U(body), nil
		}
		return convert.InstI32TruncF64S(body), nil

	// ---- Memory (slice 4) ----
	// Alignment is the *natural* alignment of the access — wasm
	// uleb-encodes it as log2(bytes). Offset is always 0 here;
	// the IR doesn't carry per-op offset (callers fold the base
	// + delta with OpAdd before the load/store).
	case ir.OpLoad:
		if op.Width == 64 {
			return memory.InstI64Load(body, 3, 0), nil
		}
		// Width=0 / 32 / WidthPtr (-1) all collapse to i32 on
		// wasm32 — WidthPtr is only meaningful on 64-bit native
		// targets. WidthString (-2) is the two-word string ABI
		// and stays out of scope until the string slice.
		if op.Width == ir.WidthString {
			return nil, fmt.Errorf("OpLoad WidthString (two-word string ABI) not yet supported")
		}
		return memory.InstI32Load(body, 2, 0), nil
	case ir.OpStore:
		if op.Width == 64 {
			return memory.InstI64Store(body, 3, 0), nil
		}
		if op.Width == ir.WidthString {
			return nil, fmt.Errorf("OpStore WidthString (two-word string ABI) not yet supported")
		}
		return memory.InstI32Store(body, 2, 0), nil
	case ir.OpFLoad:
		if op.Width == 64 {
			return memory.InstF64Load(body, 3, 0), nil
		}
		return memory.InstF32Load(body, 2, 0), nil
	case ir.OpFStore:
		if op.Width == 64 {
			return memory.InstF64Store(body, 3, 0), nil
		}
		return memory.InstF32Store(body, 2, 0), nil
	case ir.OpLoadByte:
		return memory.InstI32Load8U(body, 0, 0), nil
	case ir.OpLoadI8S:
		return memory.InstI32Load8S(body, 0, 0), nil
	case ir.OpStoreI8:
		return memory.InstI32Store8(body, 0, 0), nil
	case ir.OpLoadI16U:
		return memory.InstI32Load16U(body, 1, 0), nil
	case ir.OpLoadI16S:
		return memory.InstI32Load16S(body, 1, 0), nil
	case ir.OpStoreI16:
		return memory.InstI32Store16(body, 1, 0), nil

	// ---- Calls (slice 5) ----
	case ir.OpCallDirect:
		idx, ok := ctx.funcIdx[op.Str]
		if !ok {
			return nil, fmt.Errorf("OpCallDirect: unknown callee %q", op.Str)
		}
		return inst.InstCall(body, idx), nil

	// ---- Indirect calls (slice 6) ----
	case ir.OpCallClosureDirect:
		// Defunctionalised closure call: env_ptr is already on the
		// stack as the last arg, so this is just a direct call to
		// the hoisted target name.
		idx, ok := ctx.funcIdx[op.Str]
		if !ok {
			return nil, fmt.Errorf("OpCallClosureDirect: unknown callee %q", op.Str)
		}
		return inst.InstCall(body, idx), nil
	case ir.OpCallIndirect:
		if op.Sig == nil {
			return nil, fmt.Errorf("OpCallIndirect: missing op.Sig")
		}
		tIdx, err := ctx.addSigType(op.Sig)
		if err != nil {
			return nil, fmt.Errorf("OpCallIndirect: resolving signature: %w", err)
		}
		// table 0 (the only table in MVP); typeidx as the call
		// signature. Stack at this point: [args..., funcidx].
		return inst.InstCallIndirect(body, tIdx, 0), nil
	}
	return nil, fmt.Errorf("unsupported op %v", op.Kind)
}

// anyTableOp reports whether prog needs a table + element section.
// Indirect calls (OpCallIndirect) dispatch through the funcref
// table; OpConstFunc materialises a static table-slot pointer.
// OpCallClosureDirect doesn't dispatch through the table — its
// callee is hoisted by closure conversion — so it isn't listed
// here.
func anyTableOp(prog *ir.Program) bool {
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpCallIndirect, ir.OpConstFunc:
				return true
			}
		}
	}
	return false
}

// anyMemoryOp reports whether prog needs a memory section. Any
// load / store (including sub-width and float variants) qualifies;
// pure arithmetic / control-flow programs stay memory-free so the
// output binary is one fewer section.
func anyMemoryOp(prog *ir.Program) bool {
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpLoad, ir.OpStore,
				ir.OpFLoad, ir.OpFStore,
				ir.OpLoadByte, ir.OpStoreI8, ir.OpLoadI8S,
				ir.OpLoadI16U, ir.OpLoadI16S, ir.OpStoreI16:
				return true
			}
		}
	}
	return false
}

// blocktypeByte maps an ir.BlockType* constant to the single-byte
// blocktype encoding wasm 1.0 uses for `block` / `loop` / `if`
// when the block's result is empty or a single valtype.
//
// Multi-value blocks (string-pair, struct unpacks, etc.) need a
// typeidx reference here instead — they're out of scope for the
// control-flow slice and return an error so the missing case is
// loud.
func blocktypeByte(bt int32) (byte, error) {
	switch bt {
	case ir.BlockTypeVoid:
		return inst.BlocktypeEmpty, nil
	case ir.BlockTypeI32:
		return encode.ValtypeI32, nil
	case ir.BlockTypeI64:
		return encode.ValtypeI64, nil
	case ir.BlockTypeF32:
		return encode.ValtypeF32, nil
	case ir.BlockTypeF64:
		return encode.ValtypeF64, nil
	case ir.BlockTypeStringPair:
		return 0, fmt.Errorf("blocktype string-pair (multi-value) not yet supported")
	}
	return 0, fmt.Errorf("unknown blocktype %d", bt)
}

// appendUleb appends `v` as a uleb128 to `buf`. Duplicated from
// internal/wasm/leb128.UlebU32 to avoid a thin pass-through;
// keeping the local helper means the locals-vec assembly stays
// readable in one file.
func appendUleb(buf []byte, v uint32) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			buf = append(buf, b|0x80)
			continue
		}
		return append(buf, b)
	}
}

