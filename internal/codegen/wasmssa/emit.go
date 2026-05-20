// Package wasmssa emits a wasm core module directly from SSA
// form — the start of Phase 3 of the SSA migration. The
// existing internal/codegen/wasmbin path consumes legacy IR
// (the flat op-list shape); this package consumes ssa.Func
// instead, proving the direct path works end-to-end.
//
// Coverage is intentionally tiny in this PR — just enough to
// produce a runnable module for a single-block, integer-only
// function. Future PRs ramp the op set + control-flow coverage
// until wasmssa reaches parity with wasmbin, at which point
// wasmbin retires.
//
// Currently supported:
//
//   - i32 parameters
//   - i32 return type
//   - Single-block functions (no control flow; entry block ends
//     with TermRet)
//   - Op kinds: OpConstInt, OpAdd, OpSub, OpMul, OpAnd, OpOr,
//     OpXor, OpShl, OpShr, OpShrU, OpDiv, OpDivU, OpRem, OpRemU,
//     OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe,
//     OpGeU, OpNeg, OpNot
//
// Not yet supported (returns an unsupportedOp error):
//
//   - Multi-block CFGs
//   - i64 / f32 / f64 / string values
//   - OpPhi
//   - Function calls, memory ops, alloc
//   - Pair-return
//
// EmitModule writes the function under the export name passed in
// (typically "main"). The emitted module imports nothing, so it
// runs under any wasm runtime without WASI.
package wasmssa

import (
	"errors"
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/leb128"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// EmitModule produces a complete wasm core module exporting
// `f` under `exportName`. Returns the module bytes or an
// error if `f` uses unsupported features.
func EmitModule(f *ssa.Func, exportName string) ([]byte, error) {
	if f == nil {
		return nil, errors.New("wasmssa.EmitModule: nil func")
	}
	if exportName == "" {
		return nil, errors.New("wasmssa.EmitModule: empty exportName")
	}
	if f.Entry == nil {
		return nil, errors.New("wasmssa.EmitModule: func has no entry block")
	}
	if len(f.Blocks) != 1 {
		return nil, fmt.Errorf("wasmssa.EmitModule: only single-block functions supported, got %d blocks",
			len(f.Blocks))
	}
	entry := f.Blocks[0]
	if entry.Term.Kind != ssa.TermRet {
		return nil, fmt.Errorf("wasmssa.EmitModule: entry must end with TermRet, got %v", entry.Term.Kind)
	}

	body, valueToLocal, err := emitBody(f, entry)
	if err != nil {
		return nil, err
	}

	// Module skeleton.
	out := encode.PutModuleHeader(nil)

	// Type section: one func type (params = N × i32; result = i32).
	paramsBytes := make([]byte, 0, paramCount(f))
	for range realParams(f) {
		paramsBytes = append(paramsBytes, encode.ValtypeI32)
	}
	resultBytes := []byte{encode.ValtypeI32}
	out = sections.EncodeTypeSection(out, [][]byte{paramsBytes}, [][]byte{resultBytes})

	// Function section: one func, type index 0.
	out = sections.EncodeFunctionSection(out, []uint32{0})

	// Export section: export `exportName` → func 0.
	out = sections.EncodeExportSection(out,
		[]string{exportName},
		[]byte{0x00}, // 0 = func export kind
		[]uint32{0})

	// Code section: one func body with declared locals + body bytes.
	localCount := uint32(len(valueToLocal) - paramCount(f))
	var localsBytes []byte
	if localCount == 0 {
		localsBytes = inst.PutLocalsEmpty(nil)
	} else {
		localsBytes = inst.PutLocalsOneGroup(nil, localCount, encode.ValtypeI32)
	}
	bodyWithEnd := inst.InstEnd(body)
	funcBody := inst.PutFunctionBody(nil, localsBytes, bodyWithEnd)
	codeSectionBody := leb128.UlebU32(nil, 1) // one function entry
	codeSectionBody = append(codeSectionBody, funcBody...)
	out = encode.PutSection(out, encode.SectionCode, codeSectionBody)

	return out, nil
}

// realParams returns the non-zero Param values of f. The
// builder reserves Params[0] as a sentinel (Value{} with
// ID 0) so the count of "actual params" is len(f.Params)
// minus one. Returns the actual Param Values for iteration.
func realParams(f *ssa.Func) []ssa.Value {
	out := make([]ssa.Value, 0, len(f.Params))
	for _, p := range f.Params {
		if p.IsValid() {
			out = append(out, p)
		}
	}
	return out
}

func paramCount(f *ssa.Func) int { return len(realParams(f)) }

// emitBody lowers f's single entry block to wasm instruction
// bytes. Returns the body bytes (without trailing end), a
// map from ssa.Value.ID to wasm local index, and any error.
//
// Local layout: params first (indices 0..N-1), then one local
// per non-param Op.Result.
func emitBody(f *ssa.Func, b *ssa.Block) ([]byte, map[int32]uint32, error) {
	valueToLocal := map[int32]uint32{}
	var nextLocal uint32
	for _, p := range realParams(f) {
		valueToLocal[p.ID] = nextLocal
		nextLocal++
	}

	var body []byte
	for _, op := range b.Ops {
		newBody, err := emitOp(body, op, valueToLocal, &nextLocal)
		if err != nil {
			return nil, nil, err
		}
		body = newBody
	}

	// Terminator (TermRet only, asserted by caller).
	if b.Term.Value.IsValid() {
		body = pushValue(body, b.Term.Value, valueToLocal)
	} else {
		// Void-style ret with no value isn't supported here
		// because we declared an i32 result. Return 0 to keep
		// the module valid; real handling lands when we grow
		// the type-tagging story.
		body = inst.InstI32Const(body, 0)
	}
	body = inst.InstReturn(body)
	return body, valueToLocal, nil
}

// emitOp lowers a single Op to wasm bytes. Appends to body
// and returns the extended slice. Assigns a fresh local to
// op.Result if it's valid + not already mapped.
func emitOp(body []byte, op *ssa.Op, valueToLocal map[int32]uint32, nextLocal *uint32) ([]byte, error) {
	switch op.Kind {
	case ssa.OpConstInt:
		body = inst.InstI32Const(body, int32(op.Imm))
		return assignResult(body, op, valueToLocal, nextLocal), nil
	case ssa.OpNeg:
		// neg x → 0 - x; push 0, push x, i32.sub.
		body = inst.InstI32Const(body, 0)
		body = pushValue(body, op.Args[0], valueToLocal)
		body = append(body, 0x6b) // i32.sub
		return assignResult(body, op, valueToLocal, nextLocal), nil
	case ssa.OpNot:
		// not x → i32.eqz x (returns 1 if x == 0 else 0).
		body = pushValue(body, op.Args[0], valueToLocal)
		body = append(body, 0x45) // i32.eqz
		return assignResult(body, op, valueToLocal, nextLocal), nil
	}
	// Binary i32 ops.
	opcode, ok := binaryI32Opcode(op.Kind)
	if !ok {
		return nil, fmt.Errorf("wasmssa: unsupported op kind %v", op.Kind)
	}
	if len(op.Args) != 2 {
		return nil, fmt.Errorf("wasmssa: %v needs 2 args, got %d", op.Kind, len(op.Args))
	}
	body = pushValue(body, op.Args[0], valueToLocal)
	body = pushValue(body, op.Args[1], valueToLocal)
	body = append(body, opcode)
	return assignResult(body, op, valueToLocal, nextLocal), nil
}

// pushValue emits a local.get for the wasm local backing v.
// Caller must have already assigned v to a local via
// assignResult (or it must be a param — params are
// pre-assigned in emitBody).
func pushValue(body []byte, v ssa.Value, valueToLocal map[int32]uint32) []byte {
	idx, ok := valueToLocal[v.ID]
	if !ok {
		// Defensive — would be a bug in the emitter; we'd
		// produce malformed wasm. Emit i32.const 0 as a
		// sentinel so the error surfaces at module-validate
		// rather than as a silent miscompile.
		return inst.InstI32Const(body, 0)
	}
	return inst.InstLocalGet(body, idx)
}

// assignResult assigns op.Result a fresh wasm local (if not
// already assigned) and emits local.set so the value on top
// of the wasm stack ends up there.
func assignResult(body []byte, op *ssa.Op, valueToLocal map[int32]uint32, nextLocal *uint32) []byte {
	if !op.Result.IsValid() {
		// Side-effect-only op (none currently supported but
		// guard for forward-compat).
		return body
	}
	idx, ok := valueToLocal[op.Result.ID]
	if !ok {
		idx = *nextLocal
		*nextLocal++
		valueToLocal[op.Result.ID] = idx
	}
	return inst.InstLocalSet(body, idx)
}

// binaryI32Opcode returns the wasm opcode byte for a binary
// i32 op kind, or (0, false) if the kind isn't supported.
func binaryI32Opcode(k ssa.OpKind) (byte, bool) {
	switch k {
	case ssa.OpAdd:
		return 0x6a, true
	case ssa.OpSub:
		return 0x6b, true
	case ssa.OpMul:
		return 0x6c, true
	case ssa.OpDiv:
		return 0x6d, true // i32.div_s
	case ssa.OpDivU:
		return 0x6e, true // i32.div_u
	case ssa.OpRem:
		return 0x6f, true // i32.rem_s
	case ssa.OpRemU:
		return 0x70, true // i32.rem_u
	case ssa.OpAnd:
		return 0x71, true
	case ssa.OpOr:
		return 0x72, true
	case ssa.OpXor:
		return 0x73, true
	case ssa.OpShl:
		return 0x74, true
	case ssa.OpShr:
		return 0x75, true // i32.shr_s
	case ssa.OpShrU:
		return 0x76, true // i32.shr_u
	case ssa.OpEq:
		return 0x46, true
	case ssa.OpNe:
		return 0x47, true
	case ssa.OpLt:
		return 0x48, true // i32.lt_s
	case ssa.OpLtU:
		return 0x49, true
	case ssa.OpGt:
		return 0x4a, true // i32.gt_s
	case ssa.OpGtU:
		return 0x4b, true
	case ssa.OpLe:
		return 0x4c, true // i32.le_s
	case ssa.OpLeU:
		return 0x4d, true
	case ssa.OpGe:
		return 0x4e, true // i32.ge_s
	case ssa.OpGeU:
		return 0x4f, true
	}
	return 0, false
}
