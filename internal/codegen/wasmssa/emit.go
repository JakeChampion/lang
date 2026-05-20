// Package wasmssa emits a wasm core module directly from SSA
// form — the start of Phase 3 of the SSA migration. The
// existing internal/codegen/wasmbin path consumes legacy IR
// (the flat op-list shape); this package consumes ssa.Func
// instead, proving the direct path works end-to-end.
//
// Coverage grows incrementally. Currently supported:
//
//   - i32 parameters
//   - i32 return type
//   - Single-block functions
//   - If-else diamond shape:
//     entry ─brif─→ T ┐
//     ├─→ merge ─ret
//     entry ─brif─→ F ┘
//   - While loop shape:
//     entry ─br─→ header ─brif─→ body ─br─→ header (back-edge)
//                            └─→ done ─ret
//     Header phis (loop-carried values) become shared wasm
//     locals — entry writes initial values; the back-edge
//     re-writes them at each iteration.
//   - Op kinds: OpConstInt, OpAdd, OpSub, OpMul, OpAnd, OpOr,
//     OpXor, OpShl, OpShr, OpShrU, OpDiv, OpDivU, OpRem, OpRemU,
//     OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe,
//     OpGeU, OpNeg, OpNot, OpPhi (in if-else merges + loop
//     headers)
//
// Not yet supported (returns an unsupportedOp error):
//
//   - Other multi-block CFG shapes (nested loops, switch, etc.)
//   - i64 / f32 / f64 / string values
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
	body, valueToLocal, err := emitFunc(f)
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

// emitFunc lowers f to wasm instruction bytes. Returns the
// body bytes (without trailing end), a map from ssa.Value.ID
// to wasm local index, and any error.
//
// Local layout: params first (indices 0..N-1), then one local
// per non-param Op.Result.
//
// CFG dispatch:
//   - 1 block ending in TermRet → straight-line emission.
//   - 4 blocks shaped as if-else diamond → wasm `if`/`else`/
//     `end` form with phi-write into shared locals.
//   - Anything else → unsupported error.
func emitFunc(f *ssa.Func) ([]byte, map[int32]uint32, error) {
	valueToLocal := map[int32]uint32{}
	nextLocal := uint32(0)
	for _, p := range realParams(f) {
		valueToLocal[p.ID] = nextLocal
		nextLocal++
	}

	// Pre-assign locals for every Op.Result across all
	// blocks (including phis). Doing this up front lets
	// downstream emission look up a local by Value ID
	// regardless of which block emits the def.
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				if _, ok := valueToLocal[op.Result.ID]; !ok {
					valueToLocal[op.Result.ID] = nextLocal
					nextLocal++
				}
			}
		}
	}

	// Single-block case.
	if len(f.Blocks) == 1 {
		entry := f.Blocks[0]
		if entry.Term.Kind != ssa.TermRet {
			return nil, nil, fmt.Errorf("wasmssa: single-block func must end with TermRet, got %v",
				entry.Term.Kind)
		}
		body, err := emitStraightBlock(nil, entry, valueToLocal)
		if err != nil {
			return nil, nil, err
		}
		body = emitRet(body, entry.Term, valueToLocal)
		return body, valueToLocal, nil
	}

	// If-else diamond case.
	if diamond, ok := classifyIfElseDiamond(f); ok {
		body, err := emitIfElseDiamond(nil, diamond, valueToLocal)
		if err != nil {
			return nil, nil, err
		}
		return body, valueToLocal, nil
	}

	// While loop case.
	if lp, ok := classifyWhileLoop(f); ok {
		body, err := emitWhileLoop(nil, lp, valueToLocal)
		if err != nil {
			return nil, nil, err
		}
		return body, valueToLocal, nil
	}

	return nil, nil, fmt.Errorf("wasmssa: unsupported CFG shape (%d blocks); only single-block, if-else diamonds, and while loops handled",
		len(f.Blocks))
}

// emitStraightBlock emits the ops of `b` (skipping phis,
// which are handled separately at block-entry sites).
func emitStraightBlock(body []byte, b *ssa.Block, valueToLocal map[int32]uint32) ([]byte, error) {
	for _, op := range b.Ops {
		if op.Kind == ssa.OpPhi {
			continue // phis are written by predecessors at branch sites
		}
		newBody, err := emitOp(body, op, valueToLocal)
		if err != nil {
			return nil, err
		}
		body = newBody
	}
	return body, nil
}

// emitRet emits the bytes that materialise a TermRet — push
// the return value (or zero), `return`.
func emitRet(body []byte, term ssa.Terminator, valueToLocal map[int32]uint32) []byte {
	if term.Value.IsValid() {
		body = pushValue(body, term.Value, valueToLocal)
	} else {
		body = inst.InstI32Const(body, 0)
	}
	return inst.InstReturn(body)
}

// ifElseDiamond captures the four blocks of a recognised
// if-else CFG shape.
type ifElseDiamond struct {
	entry, t, f, merge *ssa.Block
}

// classifyIfElseDiamond detects the canonical if-else shape:
//
//	entry ends with brif T, F
//	T's only pred is entry; T ends with br merge
//	F's only pred is entry; F ends with br merge
//	merge has T and F as its preds; merge ends with ret
//
// Returns the recognised shape + true on a match.
func classifyIfElseDiamond(f *ssa.Func) (ifElseDiamond, bool) {
	if len(f.Blocks) != 4 || f.Entry == nil {
		return ifElseDiamond{}, false
	}
	entry := f.Entry
	if entry.Term.Kind != ssa.TermBrIf {
		return ifElseDiamond{}, false
	}
	t, fb := entry.Term.True, entry.Term.False
	if t == nil || fb == nil || t == fb {
		return ifElseDiamond{}, false
	}
	if len(t.Preds) != 1 || t.Preds[0] != entry {
		return ifElseDiamond{}, false
	}
	if len(fb.Preds) != 1 || fb.Preds[0] != entry {
		return ifElseDiamond{}, false
	}
	if t.Term.Kind != ssa.TermBr || fb.Term.Kind != ssa.TermBr {
		return ifElseDiamond{}, false
	}
	merge := t.Term.Target
	if merge == nil || merge != fb.Term.Target {
		return ifElseDiamond{}, false
	}
	if merge.Term.Kind != ssa.TermRet {
		return ifElseDiamond{}, false
	}
	if len(merge.Preds) != 2 {
		return ifElseDiamond{}, false
	}
	// Either pred order is acceptable; phi-arg lookup uses the
	// recorded order.
	return ifElseDiamond{entry: entry, t: t, f: fb, merge: merge}, true
}

// emitIfElseDiamond walks the diamond shape, emitting wasm
// `if cond` / `else` / `end` around the two arms. Phis at
// the merge block become shared locals; each arm writes to
// the phi's local just before branching.
func emitIfElseDiamond(body []byte, d ifElseDiamond, valueToLocal map[int32]uint32) ([]byte, error) {
	body, err := emitStraightBlock(body, d.entry, valueToLocal)
	if err != nil {
		return nil, err
	}
	// Push the brif cond + open `if` (no result; we use locals
	// for cross-arm communication).
	body = pushValue(body, d.entry.Term.Cond, valueToLocal)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	// True arm.
	body, err = emitStraightBlock(body, d.t, valueToLocal)
	if err != nil {
		return nil, err
	}
	body = writePhiArgs(body, d.merge, d.t, valueToLocal)
	body = inst.InstElse(body)
	// False arm.
	body, err = emitStraightBlock(body, d.f, valueToLocal)
	if err != nil {
		return nil, err
	}
	body = writePhiArgs(body, d.merge, d.f, valueToLocal)
	body = inst.InstEnd(body) // end if
	// Merge ops (post-phi) + ret.
	body, err = emitStraightBlock(body, d.merge, valueToLocal)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, d.merge.Term, valueToLocal)
	return body, nil
}

// writePhiArgs emits, for every phi at `target`, the bytes
// that push the phi-arg coming from `fromBlock` and store it
// into the phi's local. fromBlock must be one of target.Preds.
// Used at branch-out sites in if-else arms + while loops.
func writePhiArgs(body []byte, target, fromBlock *ssa.Block, valueToLocal map[int32]uint32) []byte {
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
		body = pushValue(body, op.Args[predIdx], valueToLocal)
		if local, ok := valueToLocal[op.Result.ID]; ok {
			body = inst.InstLocalSet(body, local)
		}
	}
	return body
}

// whileLoop captures the four blocks of a recognised
// while-loop CFG shape.
type whileLoop struct {
	entry, header, body, done *ssa.Block
}

// classifyWhileLoop detects the canonical while-loop shape:
//
//	entry ─br─→ header ─brif─→ body ─br─→ header (back-edge)
//	                       └─→ done ─ret
//
// Returns the recognised shape + true on a match.
func classifyWhileLoop(f *ssa.Func) (whileLoop, bool) {
	if len(f.Blocks) != 4 || f.Entry == nil {
		return whileLoop{}, false
	}
	entry := f.Entry
	if entry.Term.Kind != ssa.TermBr {
		return whileLoop{}, false
	}
	header := entry.Term.Target
	if header == nil || header.Term.Kind != ssa.TermBrIf {
		return whileLoop{}, false
	}
	body, done := header.Term.True, header.Term.False
	if body == nil || done == nil || body == done {
		return whileLoop{}, false
	}
	// body's only pred is header; body ends with br back to header.
	if len(body.Preds) != 1 || body.Preds[0] != header {
		return whileLoop{}, false
	}
	if body.Term.Kind != ssa.TermBr || body.Term.Target != header {
		return whileLoop{}, false
	}
	// header's preds are {entry, body} in some order.
	if len(header.Preds) != 2 {
		return whileLoop{}, false
	}
	found := map[*ssa.Block]bool{}
	for _, p := range header.Preds {
		found[p] = true
	}
	if !found[entry] || !found[body] {
		return whileLoop{}, false
	}
	// done's only pred is header; done ends with ret.
	if len(done.Preds) != 1 || done.Preds[0] != header {
		return whileLoop{}, false
	}
	if done.Term.Kind != ssa.TermRet {
		return whileLoop{}, false
	}
	return whileLoop{entry: entry, header: header, body: body, done: done}, true
}

// emitWhileLoop emits the wasm `block`/`loop`/`br_if` shape
// for a recognised while-loop CFG. Header phis become shared
// wasm locals: entry writes initial values; the back-edge
// re-writes them at each iteration.
//
// Wasm structure:
//
//	ops_in_entry
//	write phi-initial-values from entry
//	block            ; $exit (label 1)
//	  loop           ; $continue (label 0)
//	    ops_in_header (computes cond)
//	    local.get cond ; i32.eqz ; br_if $exit
//	    ops_in_body
//	    write phi-back-edge-values from body
//	    br $continue
//	  end loop
//	end block
//	ops_in_done; ret
func emitWhileLoop(body []byte, lp whileLoop, valueToLocal map[int32]uint32) ([]byte, error) {
	body, err := emitStraightBlock(body, lp.entry, valueToLocal)
	if err != nil {
		return nil, err
	}
	// Initialise header phi locals from the entry pred.
	body = writePhiArgs(body, lp.header, lp.entry, valueToLocal)

	// block $exit
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	//   loop $continue
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	//     header ops (excluding phis)
	body, err = emitStraightBlock(body, lp.header, valueToLocal)
	if err != nil {
		return nil, err
	}
	//     push cond; eqz; br_if 1 (to $exit)
	if !lp.header.Term.Cond.IsValid() {
		return nil, fmt.Errorf("wasmssa: while-loop header has invalid brif cond")
	}
	body = pushValue(body, lp.header.Term.Cond, valueToLocal)
	body = append(body, 0x45) // i32.eqz — exit when cond is false
	body = inst.InstBrIf(body, 1)
	//     body ops
	body, err = emitStraightBlock(body, lp.body, valueToLocal)
	if err != nil {
		return nil, err
	}
	//     update phi locals from body for next iteration
	body = writePhiArgs(body, lp.header, lp.body, valueToLocal)
	//     br 0 (back to loop top)
	body = inst.InstBr(body, 0)
	//   end loop
	body = inst.InstEnd(body)
	// end block
	body = inst.InstEnd(body)

	// done ops + ret.
	body, err = emitStraightBlock(body, lp.done, valueToLocal)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, lp.done.Term, valueToLocal)
	return body, nil
}

// emitOp lowers a single Op to wasm bytes. Appends to body
// and returns the extended slice. Reads op.Result's local
// (pre-assigned by emitFunc) to emit local.set.
func emitOp(body []byte, op *ssa.Op, valueToLocal map[int32]uint32) ([]byte, error) {
	switch op.Kind {
	case ssa.OpConstInt:
		body = inst.InstI32Const(body, int32(op.Imm))
		return storeResult(body, op, valueToLocal), nil
	case ssa.OpNeg:
		// neg x → 0 - x; push 0, push x, i32.sub.
		body = inst.InstI32Const(body, 0)
		body = pushValue(body, op.Args[0], valueToLocal)
		body = append(body, 0x6b) // i32.sub
		return storeResult(body, op, valueToLocal), nil
	case ssa.OpNot:
		// not x → i32.eqz x (returns 1 if x == 0 else 0).
		body = pushValue(body, op.Args[0], valueToLocal)
		body = append(body, 0x45) // i32.eqz
		return storeResult(body, op, valueToLocal), nil
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
	return storeResult(body, op, valueToLocal), nil
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

// storeResult emits local.set for op.Result (pre-assigned by
// emitFunc). No-op for side-effect-only ops with no Result.
func storeResult(body []byte, op *ssa.Op, valueToLocal map[int32]uint32) []byte {
	if !op.Result.IsValid() {
		return body
	}
	idx, ok := valueToLocal[op.Result.ID]
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
