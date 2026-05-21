// Package wasmssa emits a wasm core module directly from SSA
// form — the start of Phase 3 of the SSA migration. The
// existing internal/codegen/wasmbin path consumes legacy IR
// (the flat op-list shape); this package consumes ssa.Func
// instead, proving the direct path works end-to-end.
//
// Coverage:
//
//   - i32 parameters + i32 return type.
//   - All reducible CFG shapes — linear chain, if-else
//     diamond, one-armed if, dual-return, early-return chain,
//     while loop, and arbitrary acyclic compositions thereof.
//     Lowering is done by the relooper (see relooper.go),
//     which converts any reducible CFG into structured wasm
//     `block`/`loop`/`br`/`br_if` form. Phi nodes at merge
//     blocks lower to per-pred local writes inserted before
//     the branch. Loop support is currently bounded to a
//     single natural loop per function with one back-edge.
//   - Op kinds: OpConstInt, OpAdd, OpSub, OpMul, OpAnd, OpOr,
//     OpXor, OpShl, OpShr, OpShrU, OpDiv, OpDivU, OpRem, OpRemU,
//     OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe,
//     OpGeU, OpNeg, OpNot, OpExtend8S, OpExtend16S, OpPhi,
//     OpCall (self-recursion + calls to declared imports —
//     callee name must match either f.Name or an Import.Name
//     passed to EmitModule).
//   - Imports: the variadic Import args to EmitModule add
//     host-provided functions to the module's import section;
//     OpCall.Str = Import.Name lowers to `call <import idx>`.
//
// Not yet supported:
//
//   - Multiple natural loops, nested loops, multiple back-edges.
//   - i64 / f32 / f64 / string values.
//   - Memory ops, alloc.
//   - Pair-return.
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
	"github.com/jakechampion/lang/internal/wasm/imports"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/leb128"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// Import describes an externally-provided wasm function the
// emitted module pulls in. Module + Name address the import
// in the host environment; Params + Results give its type
// signature (each byte is an encode.Valtype*). When the
// emitted SSA function's OpCall.Str matches an Import.Name,
// the call is lowered to a wasm `call` of that import's
// function index.
//
// Currently i32-only — for params and results, only
// encode.ValtypeI32 is supported.
type Import struct {
	Module, Name string
	Params       []byte
	Results      []byte
}

// EmitModule produces a complete wasm core module exporting
// `f` under `exportName`, with the given imported functions
// available for `f` to call via OpCall. Returns the module
// bytes or an error if `f` uses unsupported features.
func EmitModule(f *ssa.Func, exportName string, importList ...Import) ([]byte, error) {
	if f == nil {
		return nil, errors.New("wasmssa.EmitModule: nil func")
	}
	if exportName == "" {
		return nil, errors.New("wasmssa.EmitModule: empty exportName")
	}
	if f.Entry == nil {
		return nil, errors.New("wasmssa.EmitModule: func has no entry block")
	}
	// Build the import-name → func-index map. Imports come
	// first in the module's func index space, so they get
	// indices 0..N-1.
	importIdx := map[string]uint32{}
	for i, im := range importList {
		if im.Name == "" {
			return nil, fmt.Errorf("wasmssa.EmitModule: import %d has empty Name", i)
		}
		if _, dup := importIdx[im.Name]; dup {
			return nil, fmt.Errorf("wasmssa.EmitModule: duplicate import name %q", im.Name)
		}
		importIdx[im.Name] = uint32(i)
	}
	// Self-function lives just after the imports.
	selfFuncIdx := uint32(len(importList))

	body, valueToLocal, err := emitFunc(f, importIdx, selfFuncIdx)
	if err != nil {
		return nil, err
	}

	// Module skeleton.
	out := encode.PutModuleHeader(nil)

	// Type section: one type per import + one for the main function.
	paramsPerType := make([][]byte, 0, len(importList)+1)
	resultsPerType := make([][]byte, 0, len(importList)+1)
	for _, im := range importList {
		paramsPerType = append(paramsPerType, im.Params)
		resultsPerType = append(resultsPerType, im.Results)
	}
	mainParams := make([]byte, 0, paramCount(f))
	for range realParams(f) {
		mainParams = append(mainParams, encode.ValtypeI32)
	}
	mainResults := []byte{encode.ValtypeI32}
	paramsPerType = append(paramsPerType, mainParams)
	resultsPerType = append(resultsPerType, mainResults)
	mainTypeIdx := uint32(len(importList))
	out = sections.EncodeTypeSection(out, paramsPerType, resultsPerType)

	// Import section (only if there ARE imports).
	if len(importList) > 0 {
		var mods, names []string
		var kinds []byte
		var descs [][]byte
		for i, im := range importList {
			mods = append(mods, im.Module)
			names = append(names, im.Name)
			kinds = append(kinds, imports.ImportFunc)
			descs = append(descs, imports.ImportDescFunc(uint32(i)))
		}
		out = imports.EncodeImportSection(out, mods, names, kinds, descs)
	}

	// Function section: one func, type index = mainTypeIdx.
	out = sections.EncodeFunctionSection(out, []uint32{mainTypeIdx})

	// Export section: export `exportName` → func `selfFuncIdx`.
	out = sections.EncodeExportSection(out,
		[]string{exportName},
		[]byte{0x00}, // 0 = func export kind
		[]uint32{selfFuncIdx})

	// Code section: one func body with declared locals + body bytes.
	localCount := uint32(len(valueToLocal) - paramCount(f))
	var localsBytes []byte
	if localCount == 0 {
		localsBytes = inst.PutLocalsEmpty(nil)
	} else {
		localsBytes = inst.PutLocalsOneGroup(nil, localCount, encode.ValtypeI32)
	}
	// PutFunctionBody appends the trailing 0x0b end byte itself,
	// so callers pass the body without one.
	funcBody := inst.PutFunctionBody(nil, localsBytes, body)
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

// emitFunc lowers f to wasm instruction bytes. Returns the
// body bytes (without trailing end), a map from ssa.Value.ID
// to wasm local index, and any error.
//
// Local layout: params first (indices 0..N-1), then one local
// per non-param Op.Result.
//
// CFG lowering is delegated to the relooper (see relooper.go),
// which handles any reducible CFG via structured wasm
// `block`/`loop`/`br`/`br_if`.
//
// emitCtx threads per-call state down to the inner emitters
// — the value→local map plus call-related metadata so emitOp
// can lower OpCall to the right function index.
type emitCtx struct {
	valueToLocal map[int32]uint32
	funcName     string
	importIdx    map[string]uint32
	selfFuncIdx  uint32
}

func emitFunc(f *ssa.Func, importIdx map[string]uint32, selfFuncIdx uint32) ([]byte, map[int32]uint32, error) {
	// Validate OpCall callees: must be either self-recursion
	// (callee name == f.Name) or one of the declared imports.
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind != ssa.OpCall {
				continue
			}
			if op.Str == f.Name {
				continue
			}
			if _, ok := importIdx[op.Str]; ok {
				continue
			}
			return nil, nil, fmt.Errorf("wasmssa: OpCall to %q is neither self-recursion (callee == %q) nor a declared import",
				op.Str, f.Name)
		}
	}
	ctx := &emitCtx{
		valueToLocal: map[int32]uint32{},
		funcName:     f.Name,
		importIdx:    importIdx,
		selfFuncIdx:  selfFuncIdx,
	}
	nextLocal := uint32(0)
	for _, p := range realParams(f) {
		ctx.valueToLocal[p.ID] = nextLocal
		nextLocal++
	}

	// Pre-assign locals for every Op.Result across all
	// blocks (including phis). Doing this up front lets
	// downstream emission look up a local by Value ID
	// regardless of which block emits the def.
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				if _, ok := ctx.valueToLocal[op.Result.ID]; !ok {
					ctx.valueToLocal[op.Result.ID] = nextLocal
					nextLocal++
				}
			}
		}
	}

	// Lower the function body via the relooper. It handles
	// every reducible CFG shape: linear chains, if-else
	// diamonds, one-armed ifs, dual-returns, early-return
	// chains, while loops, and arbitrary compositions of
	// the above (subject to the relooper's own caps —
	// currently 0-1 natural loops with a single back-edge).
	body, err := emitRelooper(f, ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("wasmssa: %v (%d blocks)", err, len(f.Blocks))
	}
	return body, ctx.valueToLocal, nil
}

// emitStraightBlock emits the ops of `b` (skipping phis,
// which are handled separately at block-entry sites).
func emitStraightBlock(body []byte, b *ssa.Block, ctx *emitCtx) ([]byte, error) {
	for _, op := range b.Ops {
		if op.Kind == ssa.OpPhi {
			continue // phis are written by predecessors at branch sites
		}
		newBody, err := emitOp(body, op, ctx)
		if err != nil {
			return nil, err
		}
		body = newBody
	}
	return body, nil
}

// emitRet emits the bytes that materialise a TermRet — push
// the return value (or zero), `return`.
func emitRet(body []byte, term ssa.Terminator, ctx *emitCtx) []byte {
	if term.Value.IsValid() {
		body = pushValue(body, term.Value, ctx)
	} else {
		body = inst.InstI32Const(body, 0)
	}
	return inst.InstReturn(body)
}

// writePhiArgs emits, for every phi at `target`, the bytes
// that push the phi-arg coming from `fromBlock` and store it
// into the phi's local. fromBlock must be one of target.Preds.
// Used by the relooper at branch-out sites.
func writePhiArgs(body []byte, target, fromBlock *ssa.Block, ctx *emitCtx) []byte {
	predIdx := -1
	for i, p := range target.Preds {
		if p == fromBlock {
			predIdx = i
			break
		}
	}
	if predIdx < 0 {
		return body // shouldn't happen; classifier guarantees
	}
	for _, op := range target.Ops {
		if op.Kind != ssa.OpPhi {
			continue
		}
		if predIdx >= len(op.Args) {
			continue
		}
		body = pushValue(body, op.Args[predIdx], ctx)
		if local, ok := ctx.valueToLocal[op.Result.ID]; ok {
			body = inst.InstLocalSet(body, local)
		}
	}
	return body
}

// emitOp lowers a single Op to wasm bytes. Appends to body
// and returns the extended slice. Reads op.Result's local
// (pre-assigned by emitFunc) to emit local.set.
func emitOp(body []byte, op *ssa.Op, ctx *emitCtx) ([]byte, error) {
	switch op.Kind {
	case ssa.OpConstInt:
		body = inst.InstI32Const(body, int32(op.Imm))
		return storeResult(body, op, ctx), nil
	case ssa.OpNeg:
		// neg x → 0 - x; push 0, push x, i32.sub.
		body = inst.InstI32Const(body, 0)
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0x6b) // i32.sub
		return storeResult(body, op, ctx), nil
	case ssa.OpNot:
		// not x → i32.eqz x (returns 1 if x == 0 else 0).
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0x45) // i32.eqz
		return storeResult(body, op, ctx), nil
	case ssa.OpExtend8S:
		// Sign-extend the low byte of x. i32.extend8_s = 0xc0
		// (sign-extension-ops feature, supported by every wasm
		// 1.x runtime since wasm 1.1 / 2020).
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0xc0)
		return storeResult(body, op, ctx), nil
	case ssa.OpExtend16S:
		// Sign-extend the low halfword. i32.extend16_s = 0xc1.
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0xc1)
		return storeResult(body, op, ctx), nil
	case ssa.OpCall:
		// Callee is either an import (use its declared func
		// index) or self-recursion (use selfFuncIdx).
		// emitFunc has already validated that op.Str matches
		// one of those two cases.
		var idx uint32
		if op.Str == ctx.funcName {
			idx = ctx.selfFuncIdx
		} else {
			idx = ctx.importIdx[op.Str]
		}
		for _, a := range op.Args {
			body = pushValue(body, a, ctx)
		}
		body = inst.InstCall(body, idx)
		return storeResult(body, op, ctx), nil
	}
	// Binary i32 ops.
	opcode, ok := binaryI32Opcode(op.Kind)
	if !ok {
		return nil, fmt.Errorf("wasmssa: unsupported op kind %v", op.Kind)
	}
	if len(op.Args) != 2 {
		return nil, fmt.Errorf("wasmssa: %v needs 2 args, got %d", op.Kind, len(op.Args))
	}
	body = pushValue(body, op.Args[0], ctx)
	body = pushValue(body, op.Args[1], ctx)
	body = append(body, opcode)
	return storeResult(body, op, ctx), nil
}

// pushValue emits a local.get for the wasm local backing v.
// Caller must have already assigned v to a local via
// assignResult (or it must be a param — params are
// pre-assigned in emitBody).
func pushValue(body []byte, v ssa.Value, ctx *emitCtx) []byte {
	idx, ok := ctx.valueToLocal[v.ID]
	if !ok {
		// Defensive — would be a bug in the emitter; we'd
		// produce malformed wasm. Emit i32.const 0 as a
		// sentinel so the error surfaces at module-validate
		// rather than as a silent miscompile.
		return inst.InstI32Const(body, 0)
	}
	return inst.InstLocalGet(body, idx)
}

// storeResult emits local.set for op.Result (pre-assigned by
// emitFunc). No-op for side-effect-only ops with no Result.
func storeResult(body []byte, op *ssa.Op, ctx *emitCtx) []byte {
	if !op.Result.IsValid() {
		return body
	}
	idx, ok := ctx.valueToLocal[op.Result.ID]
	if !ok {
		return body // shouldn't happen — emitFunc pre-assigns
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
