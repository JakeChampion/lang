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
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
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

		body, locals, err := emitBody(fn)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
		}
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
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

// emitBody walks fn.Ops and returns the function's body bytes plus
// its locals-preamble bytes (the latter pre-wrapped by
// inst.PutLocalsOneGroup-equivalent encoding for the declared local
// valtypes).
func emitBody(fn *ir.Func) (body, locals []byte, err error) {
	lvts, err := localValtypes(fn)
	if err != nil {
		return nil, nil, err
	}
	locals = encodeLocals(lvts)

	for opIdx, op := range fn.Ops {
		body, err = emitOp(body, op)
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
// to body. Op coverage is intentionally narrow for slice 1.
func emitOp(body []byte, op ir.Op) ([]byte, error) {
	switch op.Kind {
	case ir.OpConstI32:
		return inst.InstI32Const(body, op.I32), nil
	case ir.OpConstI64:
		return inst.InstI64Const(body, op.I64), nil
	case ir.OpConstF32:
		return inst.InstF32Const(body, math.Float32bits(op.F32)), nil
	case ir.OpConstF64:
		return inst.InstF64Const(body, math.Float64bits(op.F64)), nil

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
	}
	return nil, fmt.Errorf("unsupported op %v (slice 1 covers scalar arithmetic + locals + return only)", op.Kind)
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

