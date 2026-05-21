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
//   - Linear chain shape: entry → b1 → b2 → ... → ret (every
//     non-terminal block ends with `br`, last with `ret`).
//     Covers the single-block case (chain of length 1).
//   - If-else diamond shape:
//     entry ─brif─→ T ┐
//     ├─→ merge ─ret
//     entry ─brif─→ F ┘
//   - If-only (one-armed) shape:
//     entry ─brif─→ body ─br─→ merge ─ret
//     └────────────────────↗
//     The else arm is empty; entry's other brif target IS
//     the merge.
//   - Dual-return diamond:
//     entry ─brif─→ T ─ret
//     └─→ F ─ret
//     No merge — both arms return directly.
//   - While loop shape:
//     entry ─br─→ header ─brif─→ body ─br─→ header (back-edge)
//     └─→ done ─ret
//     Header phis (loop-carried values) become shared wasm
//     locals — entry writes initial values; the back-edge
//     re-writes them at each iteration.
//   - Early-return chain shape:
//     entry ─brif─→ retA ─ret
//             └─→ b1 ─brif─→ retB ─ret
//                     └─→ ... ─→ bN ─ret  (final)
//     A composition of brifs where one arm of every brif is a
//     terminal early-return block (sole pred = current, term =
//     ret) and the other arm is the next continuation. Covers
//     `if (cond) return …; if (cond2) return …; return …;`.
//   - Op kinds: OpConstInt, OpAdd, OpSub, OpMul, OpAnd, OpOr,
//     OpXor, OpShl, OpShr, OpShrU, OpDiv, OpDivU, OpRem, OpRemU,
//     OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe,
//     OpGeU, OpNeg, OpNot, OpExtend8S, OpExtend16S, OpPhi (in
//     if-else merges + loop headers), OpCall (self-recursion +
//     calls to declared imports — callee name must match either
//     f.Name or an Import.Name passed to EmitModule).
//   - Imports: the variadic Import args to EmitModule add
//     host-provided functions to the module's import section;
//     OpCall.Str = Import.Name lowers to `call <import idx>`.
//
// Not yet supported (returns an unsupportedOp error):
//
//   - Other multi-block CFG shapes (nested loops, switch, etc.)
//   - i64 / f32 / f64 / string values
//   - Memory ops, alloc
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
// CFG dispatch:
//   - Linear chain (entry → ... → ret, each block ends with
//     unconditional br to the next) → straight-line emission.
//   - 4 blocks shaped as if-else diamond → wasm `if`/`else`/
//     `end` form with phi-write into shared locals.
//   - 3 blocks shaped as one-armed if → wasm `if`/`else`/`end`
//     with the body in one arm and an empty other arm.
//   - 3 blocks shaped as dual-return diamond → wasm `if`/`else`/
//     `end` with `return` in each arm, followed by `unreachable`.
//   - 4 blocks shaped as while loop → wasm `block`/`loop`/
//     `br_if` form with phi locals carrying loop state.
//   - Anything else → unsupported error.
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

	// Single-block case or linear chain of unconditional br's
	// terminating in TermRet. Concatenate their ops + emit the
	// last block's ret.
	if chain, ok := classifyLinearChain(f); ok {
		var body []byte
		var err error
		for _, b := range chain {
			body, err = emitStraightBlock(body, b, ctx)
			if err != nil {
				return nil, nil, err
			}
		}
		last := chain[len(chain)-1]
		body = emitRet(body, last.Term, ctx)
		return body, ctx.valueToLocal, nil
	}

	// If-else diamond case.
	if diamond, ok := classifyIfElseDiamond(f); ok {
		body, err := emitIfElseDiamond(nil, diamond, ctx)
		if err != nil {
			return nil, nil, err
		}
		return body, ctx.valueToLocal, nil
	}

	// If-only (no-else) case.
	if shape, ok := classifyIfOnly(f); ok {
		body, err := emitIfOnly(nil, shape, ctx)
		if err != nil {
			return nil, nil, err
		}
		return body, ctx.valueToLocal, nil
	}

	// Dual-return diamond — both arms ret, no merge.
	if shape, ok := classifyDualReturn(f); ok {
		body, err := emitDualReturn(nil, shape, ctx)
		if err != nil {
			return nil, nil, err
		}
		return body, ctx.valueToLocal, nil
	}

	// While loop case.
	if lp, ok := classifyWhileLoop(f); ok {
		body, err := emitWhileLoop(nil, lp, ctx)
		if err != nil {
			return nil, nil, err
		}
		return body, ctx.valueToLocal, nil
	}

	// Early-return chain — `if (a) return …; if (b) return …;
	// return …;` and friends. Strictly more general than
	// dualReturn, but placed after it so the simpler classifier
	// keeps owning the 3-block case.
	if chain, ok := classifyEarlyReturnChain(f); ok {
		body, err := emitEarlyReturnChain(nil, chain, ctx)
		if err != nil {
			return nil, nil, err
		}
		return body, ctx.valueToLocal, nil
	}

	// Relooper fallback — handles arbitrary acyclic reducible
	// CFGs by lowering to nested `block` wraps. Each classifier
	// above produces tighter wasm than the relooper for its
	// shape; the relooper picks up everything else.
	body, err := emitRelooper(f, ctx)
	if err == nil {
		return body, ctx.valueToLocal, nil
	}
	return nil, nil, fmt.Errorf("wasmssa: unsupported CFG shape (%d blocks); relooper rejected: %v",
		len(f.Blocks), err)
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

// classifyLinearChain walks the CFG starting at f.Entry,
// following unconditional TermBr edges. If every block in
// the walk has exactly one pred (except entry) and the chain
// terminates with a TermRet (and no other block exists), it
// returns the chain in walk order. Returns nil, false on
// any deviation (branching, back-edges, dangling blocks).
//
// Covers both the single-block case (entry ends with ret)
// and the multi-block straight line the lifter sometimes
// emits (e.g. entry → const-init → ret).
func classifyLinearChain(f *ssa.Func) ([]*ssa.Block, bool) {
	if f.Entry == nil {
		return nil, false
	}
	chain := []*ssa.Block{f.Entry}
	seen := map[*ssa.Block]bool{f.Entry: true}
	cur := f.Entry
	for cur.Term.Kind == ssa.TermBr {
		nxt := cur.Term.Target
		if nxt == nil || seen[nxt] {
			return nil, false // back-edge or nil target
		}
		// Each non-entry block must have exactly one pred —
		// otherwise some other path could reach it and the
		// straight-line emission would miss them.
		if len(nxt.Preds) != 1 || nxt.Preds[0] != cur {
			return nil, false
		}
		chain = append(chain, nxt)
		seen[nxt] = true
		cur = nxt
	}
	if cur.Term.Kind != ssa.TermRet {
		return nil, false
	}
	// Every block in f.Blocks must be in the chain — otherwise
	// there are orphan blocks we're ignoring.
	if len(chain) != len(f.Blocks) {
		return nil, false
	}
	return chain, true
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
func emitIfElseDiamond(body []byte, d ifElseDiamond, ctx *emitCtx) ([]byte, error) {
	body, err := emitStraightBlock(body, d.entry, ctx)
	if err != nil {
		return nil, err
	}
	// Push the brif cond + open `if` (no result; we use locals
	// for cross-arm communication).
	body = pushValue(body, d.entry.Term.Cond, ctx)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	// True arm.
	body, err = emitStraightBlock(body, d.t, ctx)
	if err != nil {
		return nil, err
	}
	body = writePhiArgs(body, d.merge, d.t, ctx)
	body = inst.InstElse(body)
	// False arm.
	body, err = emitStraightBlock(body, d.f, ctx)
	if err != nil {
		return nil, err
	}
	body = writePhiArgs(body, d.merge, d.f, ctx)
	body = inst.InstEnd(body) // end if
	// Merge ops (post-phi) + ret.
	body, err = emitStraightBlock(body, d.merge, ctx)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, d.merge.Term, ctx)
	return body, nil
}

// writePhiArgs emits, for every phi at `target`, the bytes
// that push the phi-arg coming from `fromBlock` and store it
// into the phi's local. fromBlock must be one of target.Preds.
// Used at branch-out sites in if-else arms + while loops.
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

// ifOnly captures the three blocks of a one-armed if shape
// (no else): entry's True branch enters a body, False
// branch falls through directly to merge.
type ifOnly struct {
	entry, body, merge *ssa.Block
}

// classifyIfOnly detects the canonical one-armed-if shape:
//
//	entry ─brif─→ body ─br─→ merge ─ret
//	   └────────────────────↗   (False edge of brif)
//
// body's only pred is entry; merge has both entry and body
// as preds; merge ends with ret. Either True or False of
// entry's brif may be `body` — the False arm contributes
// only the no-op flow into merge.
func classifyIfOnly(f *ssa.Func) (ifOnly, bool) {
	if len(f.Blocks) != 3 || f.Entry == nil {
		return ifOnly{}, false
	}
	entry := f.Entry
	if entry.Term.Kind != ssa.TermBrIf {
		return ifOnly{}, false
	}
	t, fb := entry.Term.True, entry.Term.False
	if t == nil || fb == nil || t == fb {
		return ifOnly{}, false
	}
	// One of {t, fb} is the body (with exactly entry as pred,
	// ending in br merge); the other IS the merge (which
	// entry's False/True edge enters directly).
	var body, merge *ssa.Block
	switch {
	case len(t.Preds) == 1 && t.Preds[0] == entry &&
		t.Term.Kind == ssa.TermBr && t.Term.Target == fb:
		body, merge = t, fb
	case len(fb.Preds) == 1 && fb.Preds[0] == entry &&
		fb.Term.Kind == ssa.TermBr && fb.Term.Target == t:
		body, merge = fb, t
	default:
		return ifOnly{}, false
	}
	if merge.Term.Kind != ssa.TermRet {
		return ifOnly{}, false
	}
	if len(merge.Preds) != 2 {
		return ifOnly{}, false
	}
	return ifOnly{entry: entry, body: body, merge: merge}, true
}

// emitIfOnly emits the one-armed-if shape. The wasm `if`
// block contains the body's ops; the implicit else is empty.
// Phi-arg writes go in the appropriate slot — true-arm if
// entry's True == body, false-arm if entry's False == body.
func emitIfOnly(body []byte, s ifOnly, ctx *emitCtx) ([]byte, error) {
	body, err := emitStraightBlock(body, s.entry, ctx)
	if err != nil {
		return nil, err
	}
	// Push cond. If entry's True == s.body, the if-body runs
	// when cond is true (no flip needed). If entry's False ==
	// s.body, flip the cond via i32.eqz so the wasm `if`
	// enters when the original False arm should.
	body = pushValue(body, s.entry.Term.Cond, ctx)
	if s.entry.Term.False == s.body {
		body = append(body, 0x45) // i32.eqz
	}
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	// Body arm.
	body, err = emitStraightBlock(body, s.body, ctx)
	if err != nil {
		return nil, err
	}
	body = writePhiArgs(body, s.merge, s.body, ctx)
	body = inst.InstElse(body)
	// Else arm: no ops, just phi-writes from entry.
	body = writePhiArgs(body, s.merge, s.entry, ctx)
	body = inst.InstEnd(body)
	// Merge ops + ret.
	body, err = emitStraightBlock(body, s.merge, ctx)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, s.merge.Term, ctx)
	return body, nil
}

// dualReturn captures a 3-block CFG where entry brifs to two
// arms, both ending in TermRet — no merge.
type dualReturn struct {
	entry, t, f *ssa.Block
}

// classifyDualReturn detects:
//
//	entry ─brif─→ T ─ret
//	        └─→ F ─ret
//
// T and F are independent return paths; both have entry as
// their sole pred.
func classifyDualReturn(f *ssa.Func) (dualReturn, bool) {
	if len(f.Blocks) != 3 || f.Entry == nil {
		return dualReturn{}, false
	}
	entry := f.Entry
	if entry.Term.Kind != ssa.TermBrIf {
		return dualReturn{}, false
	}
	t, fb := entry.Term.True, entry.Term.False
	if t == nil || fb == nil || t == fb {
		return dualReturn{}, false
	}
	if len(t.Preds) != 1 || t.Preds[0] != entry {
		return dualReturn{}, false
	}
	if len(fb.Preds) != 1 || fb.Preds[0] != entry {
		return dualReturn{}, false
	}
	if t.Term.Kind != ssa.TermRet || fb.Term.Kind != ssa.TermRet {
		return dualReturn{}, false
	}
	return dualReturn{entry: entry, t: t, f: fb}, true
}

// emitDualReturn emits a wasm `if`/`else`/`end` where each
// arm executes its block's ops, pushes its ret value, and
// emits `return`. After the `if/end` the function body is
// unreachable; we emit a trailing `unreachable` + an i32.const 0
// to keep the stack-balance check happy.
func emitDualReturn(body []byte, d dualReturn, ctx *emitCtx) ([]byte, error) {
	body, err := emitStraightBlock(body, d.entry, ctx)
	if err != nil {
		return nil, err
	}
	body = pushValue(body, d.entry.Term.Cond, ctx)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)

	body, err = emitStraightBlock(body, d.t, ctx)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, d.t.Term, ctx)

	body = inst.InstElse(body)

	body, err = emitStraightBlock(body, d.f, ctx)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, d.f.Term, ctx)

	body = inst.InstEnd(body) // end if

	// Both arms `return`, so the fallthrough after the if/end
	// can never execute. `unreachable` puts the wasm validator
	// in stack-polymorphic mode, which satisfies the function
	// body's result-type requirement without us needing to push
	// a placeholder value.
	body = inst.InstUnreachable(body)
	return body, nil
}

// whileLoop captures the four blocks of a recognised
// while-loop CFG shape.
type whileLoop struct {
	entry, header, body, done *ssa.Block
	// bodyOnTrueArm is true when the header's `brif True`
	// target is `body` (loop while cond), false when the
	// True target is `done` (exit when cond — emit needs to
	// flip via i32.eqz so the wasm `br_if $exit` triggers
	// on the right polarity).
	bodyOnTrueArm bool
}

// classifyWhileLoop detects the canonical while-loop shape:
//
//	entry ─br─→ header ─brif─→ body ─br─→ header (back-edge)
//	                       └─→ done ─ret
//
// Accepts either brif arrangement — body may be on the True
// arm (loop while cond) or the False arm (loop while !cond,
// the optimizer can flip the comparison via CmpFlip + remove
// not's leaving the exit on True).
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
	t, fb := header.Term.True, header.Term.False
	if t == nil || fb == nil || t == fb {
		return whileLoop{}, false
	}
	// Figure out which arm holds the body (sole pred = header,
	// terminator = br back to header) and which holds done
	// (sole pred = header, terminator = ret).
	var body, done *ssa.Block
	bodyOnTrueArm := false
	switch {
	case isLoopBody(t, header) && isLoopDone(fb, header):
		body, done = t, fb
		bodyOnTrueArm = true
	case isLoopBody(fb, header) && isLoopDone(t, header):
		body, done = fb, t
		bodyOnTrueArm = false
	default:
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
	return whileLoop{entry: entry, header: header, body: body, done: done, bodyOnTrueArm: bodyOnTrueArm}, true
}

// isLoopBody reports whether `b` has the shape of a while-
// loop body: only pred is `header`, terminator is `br
// header`.
func isLoopBody(b, header *ssa.Block) bool {
	if b == nil {
		return false
	}
	if len(b.Preds) != 1 || b.Preds[0] != header {
		return false
	}
	return b.Term.Kind == ssa.TermBr && b.Term.Target == header
}

// isLoopDone reports whether `b` has the shape of a while-
// loop done block: only pred is `header`, terminator is ret.
func isLoopDone(b, header *ssa.Block) bool {
	if b == nil {
		return false
	}
	if len(b.Preds) != 1 || b.Preds[0] != header {
		return false
	}
	return b.Term.Kind == ssa.TermRet
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
func emitWhileLoop(body []byte, lp whileLoop, ctx *emitCtx) ([]byte, error) {
	body, err := emitStraightBlock(body, lp.entry, ctx)
	if err != nil {
		return nil, err
	}
	// Initialise header phi locals from the entry pred.
	body = writePhiArgs(body, lp.header, lp.entry, ctx)

	// block $exit
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	//   loop $continue
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	//     header ops (excluding phis)
	body, err = emitStraightBlock(body, lp.header, ctx)
	if err != nil {
		return nil, err
	}
	//     push cond; br_if $exit. When the body sits on the
	//     True arm, the loop continues if cond is true → exit
	//     if cond is false → emit i32.eqz. When body is on the
	//     False arm, the loop continues if cond is false →
	//     exit if cond is true → no flip.
	if !lp.header.Term.Cond.IsValid() {
		return nil, fmt.Errorf("wasmssa: while-loop header has invalid brif cond")
	}
	body = pushValue(body, lp.header.Term.Cond, ctx)
	if lp.bodyOnTrueArm {
		body = append(body, 0x45) // i32.eqz — exit when cond is false
	}
	body = inst.InstBrIf(body, 1)
	//     body ops
	body, err = emitStraightBlock(body, lp.body, ctx)
	if err != nil {
		return nil, err
	}
	//     update phi locals from body for next iteration
	body = writePhiArgs(body, lp.header, lp.body, ctx)
	//     br 0 (back to loop top)
	body = inst.InstBr(body, 0)
	//   end loop
	body = inst.InstEnd(body)
	// end block
	body = inst.InstEnd(body)

	// done ops + ret.
	body, err = emitStraightBlock(body, lp.done, ctx)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, lp.done.Term, ctx)
	return body, nil
}

// earlyReturnStep records one early-return brif in a chain.
// `block` holds the cond + ops; `retBlock` is the immediate-
// ret target of one brif arm. `retOnTrueArm` flags which arm
// of `block.Term` `retBlock` sits on — emit flips the cond via
// i32.eqz when it's on False so the wasm `if` enters when the
// original True arm should have.
type earlyReturnStep struct {
	block         *ssa.Block
	retBlock      *ssa.Block
	retOnTrueArm  bool
}

// earlyReturnChain is the recognised CFG: a sequence of
// brif-steps each shedding one early-return arm, ending at a
// final block whose terminator is ret.
type earlyReturnChain struct {
	steps []earlyReturnStep
	final *ssa.Block
}

// classifyEarlyReturnChain walks from f.Entry following the
// continuation arm of each brif. At each step it expects:
//
//   - Current block ends with TermBrIf.
//   - One arm is a terminal early-return block (sole pred is
//     current, terminator is ret).
//   - Other arm is the next continuation (sole pred is current).
//
// The walk terminates when the current block's terminator is
// TermRet. The classifier requires len(f.Blocks) >= 5 so it
// leaves the 3-block dualReturn case to the simpler classifier;
// every block in f.Blocks must be exactly one of {step.block,
// step.retBlock, final}.
func classifyEarlyReturnChain(f *ssa.Func) (earlyReturnChain, bool) {
	if f.Entry == nil {
		return earlyReturnChain{}, false
	}
	if len(f.Blocks) < 5 {
		return earlyReturnChain{}, false
	}
	var chain earlyReturnChain
	visited := map[*ssa.Block]bool{f.Entry: true}
	cur := f.Entry
	for {
		switch cur.Term.Kind {
		case ssa.TermRet:
			chain.final = cur
			// Every block accounted for: 2 per step (block +
			// retBlock) plus the final.
			if 2*len(chain.steps)+1 != len(f.Blocks) {
				return earlyReturnChain{}, false
			}
			if len(chain.steps) == 0 {
				// Pure linear walk — handled by classifyLinearChain.
				return earlyReturnChain{}, false
			}
			return chain, true
		case ssa.TermBrIf:
			t, fb := cur.Term.True, cur.Term.False
			if t == nil || fb == nil || t == fb {
				return earlyReturnChain{}, false
			}
			var ret, next *ssa.Block
			retOnTrue := false
			switch {
			case isTerminalRet(t, cur) && isSolePredOf(fb, cur):
				ret, next, retOnTrue = t, fb, true
			case isTerminalRet(fb, cur) && isSolePredOf(t, cur):
				ret, next, retOnTrue = fb, t, false
			default:
				return earlyReturnChain{}, false
			}
			if visited[next] || visited[ret] {
				return earlyReturnChain{}, false
			}
			chain.steps = append(chain.steps, earlyReturnStep{
				block:        cur,
				retBlock:     ret,
				retOnTrueArm: retOnTrue,
			})
			visited[next] = true
			visited[ret] = true
			cur = next
		default:
			return earlyReturnChain{}, false
		}
	}
}

// isTerminalRet reports whether `b` is a sole-pred-of-`pred`
// block whose terminator is ret — the shape of an early-return
// arm.
func isTerminalRet(b, pred *ssa.Block) bool {
	if b == nil {
		return false
	}
	if len(b.Preds) != 1 || b.Preds[0] != pred {
		return false
	}
	return b.Term.Kind == ssa.TermRet
}

// isSolePredOf reports whether `b`'s only pred is `pred`.
func isSolePredOf(b, pred *ssa.Block) bool {
	if b == nil {
		return false
	}
	return len(b.Preds) == 1 && b.Preds[0] == pred
}

// emitEarlyReturnChain lowers the chain as a flat sequence of
// `if cond ... return end` segments — each wasm `if` body
// emits the early-return arm's ops + a wasm `return`, so
// control naturally falls through into the next continuation
// when the cond is false.
//
//	step.block.ops
//	push cond [i32.eqz if early-ret is on False arm]
//	if
//	  step.retBlock.ops
//	  return
//	end
//	... next step ...
//	final.ops
//	return
func emitEarlyReturnChain(body []byte, chain earlyReturnChain, ctx *emitCtx) ([]byte, error) {
	for _, step := range chain.steps {
		var err error
		body, err = emitStraightBlock(body, step.block, ctx)
		if err != nil {
			return nil, err
		}
		if !step.block.Term.Cond.IsValid() {
			return nil, fmt.Errorf("wasmssa: early-return chain brif has invalid cond")
		}
		body = pushValue(body, step.block.Term.Cond, ctx)
		if !step.retOnTrueArm {
			body = append(body, 0x45) // i32.eqz — flip so wasm `if` enters on the early-ret arm
		}
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body, err = emitStraightBlock(body, step.retBlock, ctx)
		if err != nil {
			return nil, err
		}
		body = emitRet(body, step.retBlock.Term, ctx)
		body = inst.InstEnd(body)
	}
	body, err := emitStraightBlock(body, chain.final, ctx)
	if err != nil {
		return nil, err
	}
	body = emitRet(body, chain.final.Term, ctx)
	return body, nil
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
