// Package wasmssa emits a wasm core module directly from SSA
// form — the start of Phase 3 of the SSA migration. The
// existing internal/codegen/wasmbin path consumes legacy IR
// (the flat op-list shape); this package consumes ssa.Func
// instead, proving the direct path works end-to-end.
//
// Coverage:
//
//   - i32 and i64 parameters + return types. Width is read from
//     ssa.Func.ParamWidths / ReturnWidth; emit picks between
//     i32.* / i64.* opcodes per value's inferred width.
//   - All reducible CFG shapes — linear chain, if-else
//     diamond, one-armed if, dual-return, early-return chain,
//     while loop, and arbitrary acyclic compositions thereof.
//     Lowering is done by the relooper (see relooper.go),
//     which converts any reducible CFG into structured wasm
//     `block`/`loop`/`br`/`br_if` form. Phi nodes at merge
//     blocks lower to per-pred local writes inserted before
//     the branch. Supports multiple sequential + nested loops
//     with single back-edges.
//   - Op kinds: OpConstInt, OpAdd, OpSub, OpMul, OpAnd, OpOr,
//     OpXor, OpShl, OpShr, OpShrU, OpDiv, OpDivU, OpRem, OpRemU,
//     OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe,
//     OpGeU, OpNeg, OpNot, OpExtend8S, OpExtend16S, OpPhi,
//     OpCall (self-recursion + calls to declared imports),
//     OpLoad/OpStore + sub-word variants (OpLoad8S/U, OpLoad16S/U,
//     OpStore8, OpStore16), OpAlloc (bump allocator).
//   - Imports: the variadic Import args to EmitModule add
//     host-provided functions to the module's import section;
//     OpCall.Str = Import.Name lowers to `call <import idx>`.
//   - Memory: a 1-page linear memory + single global (the bump
//     allocator's heap-top) are emitted only when the function
//     uses any memory or alloc op. Heap starts at byte 1024
//     (leaves room for future static data).
//
// Not yet supported:
//
//   - f32 / f64 / string values.
//   - Float memory ops (OpLoadF, OpStoreF).
//   - Multi-page memory + memory.grow.
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
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// heapGlobalIdx is the wasm global index of the bump-allocator
// "heap top" pointer. Always 0 in modules that emit memory
// support — wasmssa only ever declares this single global.
const heapGlobalIdx uint32 = 0

// heapInitOffset is the byte offset at which the bump allocator
// starts handing out memory. Leaves a small reserved region at
// the bottom for static data (currently unused; placeholder for
// future const-string + literal-data work).
const heapInitOffset = 1024

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

	body, valueToLocal, valueWidth, err := emitFunc(f, importIdx, selfFuncIdx)
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
	rparams := realParams(f)
	mainParams := make([]byte, 0, len(rparams))
	for i := range rparams {
		if i < len(f.ParamWidths) && f.ParamWidths[i] == 64 {
			mainParams = append(mainParams, encode.ValtypeI64)
		} else {
			mainParams = append(mainParams, encode.ValtypeI32)
		}
	}
	var mainResults []byte
	if f.ReturnWidth == 64 {
		mainResults = []byte{encode.ValtypeI64}
	} else {
		mainResults = []byte{encode.ValtypeI32}
	}
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

	// Memory + global sections: emitted only if the function
	// uses any memory op or alloc. One linear-memory page is
	// plenty for the test cases the relooper currently exercises;
	// future PRs can grow it dynamically or precompute high-water
	// usage. The global is the bump-allocator's heap-top pointer.
	if usesMemory(f) {
		out = sections.EncodeMemorySection(out, 1, -1)
		initExpr := inst.InstEnd(inst.InstI32Const(nil, heapInitOffset))
		out = sections.EncodeGlobalSection(out,
			[]byte{encode.ValtypeI32},
			[]byte{0x01}, // mutable
			[][]byte{initExpr})
	}

	// Export section: export `exportName` → func `selfFuncIdx`.
	out = sections.EncodeExportSection(out,
		[]string{exportName},
		[]byte{0x00}, // 0 = func export kind
		[]uint32{selfFuncIdx})

	// Code section: one func body with declared locals + body bytes.
	// Locals are grouped by valtype — group consecutive same-type
	// runs to stay compact; the wasm format only requires that
	// each group is (count, valtype) and the local indices match
	// the declaration order.
	localsBytes := encodeLocals(valueToLocal, valueWidth, paramCount(f))
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
	// valueWidth maps a Value.ID to its bit-width (32 or 64).
	// Built once per emitFunc by walking ops + param widths so
	// emitOp can pick i32 vs i64 opcodes per use.
	valueWidth  map[int32]int8
	funcName    string
	importIdx   map[string]uint32
	selfFuncIdx uint32
	// returnWidth is the bit-width of the function's return
	// (32 or 64). Used by emitRet to pick the right placeholder
	// when TermRet has no value.
	returnWidth int8
}

func emitFunc(f *ssa.Func, importIdx map[string]uint32, selfFuncIdx uint32) ([]byte, map[int32]uint32, map[int32]int8, error) {
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
			return nil, nil, nil, fmt.Errorf("wasmssa: OpCall to %q is neither self-recursion (callee == %q) nor a declared import",
				op.Str, f.Name)
		}
	}
	rw := int8(32)
	if f.ReturnWidth == 64 {
		rw = 64
	}
	ctx := &emitCtx{
		valueToLocal: map[int32]uint32{},
		valueWidth:   map[int32]int8{},
		funcName:     f.Name,
		importIdx:    importIdx,
		selfFuncIdx:  selfFuncIdx,
		returnWidth:  rw,
	}
	nextLocal := uint32(0)
	rparams := realParams(f)
	for i, p := range rparams {
		ctx.valueToLocal[p.ID] = nextLocal
		nextLocal++
		w := int8(32)
		if i < len(f.ParamWidths) && f.ParamWidths[i] == 64 {
			w = 64
		}
		ctx.valueWidth[p.ID] = w
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
				ctx.valueWidth[op.Result.ID] = inferResultWidth(op, ctx.valueWidth)
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
		return nil, nil, nil, fmt.Errorf("wasmssa: %v (%d blocks)", err, len(f.Blocks))
	}
	return body, ctx.valueToLocal, ctx.valueWidth, nil
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
// the return value (or a zero placeholder of the right valtype),
// then `return`.
func emitRet(body []byte, term ssa.Terminator, ctx *emitCtx) []byte {
	if term.Value.IsValid() {
		body = pushValue(body, term.Value, ctx)
	} else if ctx.returnWidth == 64 {
		body = inst.InstI64Const(body, 0)
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

// encodeLocals builds the locals_vec for a function body.
// Walks local indices [paramCount, max] in order, grouping
// consecutive same-valtype runs into wasm `(count, valtype)`
// pairs. Handles the all-i32, all-i64, and mixed cases.
func encodeLocals(valueToLocal map[int32]uint32, valueWidth map[int32]int8, nParams int) []byte {
	if len(valueToLocal) == nParams {
		return inst.PutLocalsEmpty(nil)
	}
	// Build idx → width array (skipping params).
	max := uint32(0)
	for _, idx := range valueToLocal {
		if idx > max {
			max = idx
		}
	}
	widthByIdx := make([]int8, max+1)
	for vID, idx := range valueToLocal {
		if valueWidth[vID] == 64 {
			widthByIdx[idx] = 64
		} else {
			widthByIdx[idx] = 32
		}
	}
	// Group consecutive same-width runs over the non-param range.
	type group struct {
		count uint32
		vt    byte
	}
	var groups []group
	for i := uint32(nParams); i <= max; i++ {
		var vt byte = encode.ValtypeI32
		if widthByIdx[i] == 64 {
			vt = encode.ValtypeI64
		}
		if len(groups) > 0 && groups[len(groups)-1].vt == vt {
			groups[len(groups)-1].count++
		} else {
			groups = append(groups, group{count: 1, vt: vt})
		}
	}
	buf := leb128.UlebU32(nil, uint32(len(groups)))
	for _, g := range groups {
		buf = leb128.UlebU32(buf, g.count)
		buf = append(buf, g.vt)
	}
	return buf
}

// inferResultWidth derives the bit-width (32 or 64) of `op.Result`
// from the op kind + the widths of its args. Used by emitFunc to
// build the value-width map so emitOp can pick i32 vs i64 opcodes.
func inferResultWidth(op *ssa.Op, widthOf map[int32]int8) int8 {
	switch op.Kind {
	case ssa.OpExtendS, ssa.OpExtendU:
		// i32 → i64 sign/zero extend.
		return 64
	case ssa.OpTrunc:
		// i64 → i32.
		return 32
	case ssa.OpExtend8S, ssa.OpExtend16S:
		// i32 → i32 (sign-extend low byte/halfword).
		return 32
	case ssa.OpEq, ssa.OpNe,
		ssa.OpLt, ssa.OpLtU, ssa.OpLe, ssa.OpLeU,
		ssa.OpGt, ssa.OpGtU, ssa.OpGe, ssa.OpGeU,
		ssa.OpNot:
		// Boolean results are always i32.
		return 32
	case ssa.OpConstInt:
		if op.Width == 64 {
			return 64
		}
		return 32
	case ssa.OpAdd, ssa.OpSub, ssa.OpMul,
		ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU,
		ssa.OpAnd, ssa.OpOr, ssa.OpXor,
		ssa.OpShl, ssa.OpShr, ssa.OpShrU,
		ssa.OpNeg:
		if op.Width == 64 {
			return 64
		}
		// Fall back to args' width — handles cases where the
		// lift didn't stamp Width (older ops keep width from
		// their operands).
		for _, a := range op.Args {
			if widthOf[a.ID] == 64 {
				return 64
			}
		}
		return 32
	case ssa.OpPhi:
		// Phi inherits width from its incoming args (which all
		// agree on width — verified at SSA build time).
		for _, a := range op.Args {
			if a.IsValid() {
				if widthOf[a.ID] == 64 {
					return 64
				}
			}
		}
		return 32
	case ssa.OpLoad, ssa.OpLoad8S, ssa.OpLoad8U, ssa.OpLoad16S, ssa.OpLoad16U:
		return 32
	case ssa.OpAlloc:
		return 32 // alloc returns a wasm32 pointer
	case ssa.OpCall:
		// Result width matches the callee's return width. The
		// emitter doesn't currently know that — assume i32. (For
		// self-recursion we COULD look at f.ReturnWidth but the
		// inference happens per-op before that's threaded; keep
		// it conservative.)
		return 32
	}
	return 32
}

// usesMemory reports whether `f` contains any op that touches
// linear memory or the bump allocator. Cheap O(ops) scan —
// used by EmitModule to decide whether to emit the memory +
// global sections.
func usesMemory(f *ssa.Func) bool {
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			switch op.Kind {
			case ssa.OpLoad, ssa.OpStore,
				ssa.OpLoad8S, ssa.OpLoad8U,
				ssa.OpLoad16S, ssa.OpLoad16U,
				ssa.OpStore8, ssa.OpStore16,
				ssa.OpAlloc:
				return true
			}
		}
	}
	return false
}

// memarg widths (log2 alignment) for the i32 load/store family.
const (
	memAlignByte = 0 // 8-bit access
	memAlignHalf = 1 // 16-bit access
	memAlignWord = 2 // 32-bit access
)

// emitOp lowers a single Op to wasm bytes. Appends to body
// and returns the extended slice. Reads op.Result's local
// (pre-assigned by emitFunc) to emit local.set.
func emitOp(body []byte, op *ssa.Op, ctx *emitCtx) ([]byte, error) {
	switch op.Kind {
	case ssa.OpConstInt:
		if op.Width == 64 {
			body = inst.InstI64Const(body, op.Imm)
		} else {
			body = inst.InstI32Const(body, int32(op.Imm))
		}
		return storeResult(body, op, ctx), nil
	case ssa.OpNeg:
		// neg x → 0 - x; push 0, push x, sub.
		argW := ctx.valueWidth[op.Args[0].ID]
		if op.Width == 64 || argW == 64 {
			body = inst.InstI64Const(body, 0)
			body = pushValue(body, op.Args[0], ctx)
			body = numeric.InstI64Sub(body)
		} else {
			body = inst.InstI32Const(body, 0)
			body = pushValue(body, op.Args[0], ctx)
			body = append(body, 0x6b) // i32.sub
		}
		return storeResult(body, op, ctx), nil
	case ssa.OpNot:
		// not x → eqz x (returns 1 if x == 0 else 0). Pick i64.eqz
		// vs i32.eqz from the arg's width; the result is always i32.
		body = pushValue(body, op.Args[0], ctx)
		if ctx.valueWidth[op.Args[0].ID] == 64 {
			body = append(body, 0x50) // i64.eqz → i32
		} else {
			body = append(body, 0x45) // i32.eqz
		}
		return storeResult(body, op, ctx), nil
	case ssa.OpExtendS:
		// i32 → i64 sign-extend. wasm: i64.extend_i32_s = 0xac.
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0xac)
		return storeResult(body, op, ctx), nil
	case ssa.OpExtendU:
		// i32 → i64 zero-extend. wasm: i64.extend_i32_u = 0xad.
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0xad)
		return storeResult(body, op, ctx), nil
	case ssa.OpTrunc:
		// i64 → i32 (keep low 32 bits). wasm: i32.wrap_i64 = 0xa7.
		body = pushValue(body, op.Args[0], ctx)
		body = append(body, 0xa7)
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
	case ssa.OpLoad:
		// load i32 from memory[arg0 + imm]. Args[0] is the
		// base pointer; op.Imm is the constant byte offset.
		body = pushValue(body, op.Args[0], ctx)
		body = memory.InstI32Load(body, memAlignWord, uint32(op.Imm))
		return storeResult(body, op, ctx), nil
	case ssa.OpLoad8S:
		body = pushValue(body, op.Args[0], ctx)
		body = memory.InstI32Load8S(body, memAlignByte, uint32(op.Imm))
		return storeResult(body, op, ctx), nil
	case ssa.OpLoad8U:
		body = pushValue(body, op.Args[0], ctx)
		body = memory.InstI32Load8U(body, memAlignByte, uint32(op.Imm))
		return storeResult(body, op, ctx), nil
	case ssa.OpLoad16S:
		body = pushValue(body, op.Args[0], ctx)
		body = memory.InstI32Load16S(body, memAlignHalf, uint32(op.Imm))
		return storeResult(body, op, ctx), nil
	case ssa.OpLoad16U:
		body = pushValue(body, op.Args[0], ctx)
		body = memory.InstI32Load16U(body, memAlignHalf, uint32(op.Imm))
		return storeResult(body, op, ctx), nil
	case ssa.OpStore:
		// store i32 value Args[1] to memory[Args[0] + imm].
		body = pushValue(body, op.Args[0], ctx)
		body = pushValue(body, op.Args[1], ctx)
		body = memory.InstI32Store(body, memAlignWord, uint32(op.Imm))
		return body, nil // no result
	case ssa.OpStore8:
		body = pushValue(body, op.Args[0], ctx)
		body = pushValue(body, op.Args[1], ctx)
		body = memory.InstI32Store8(body, memAlignByte, uint32(op.Imm))
		return body, nil
	case ssa.OpStore16:
		body = pushValue(body, op.Args[0], ctx)
		body = pushValue(body, op.Args[1], ctx)
		body = memory.InstI32Store16(body, memAlignHalf, uint32(op.Imm))
		return body, nil
	case ssa.OpAlloc:
		// Bump allocator: push current heap_top (the result),
		// push (heap_top + size), store back. Args[0] = size.
		body = inst.InstGlobalGet(body, heapGlobalIdx)         // result (old top)
		body = inst.InstGlobalGet(body, heapGlobalIdx)         // for the bump
		body = pushValue(body, op.Args[0], ctx)                // size
		body = append(body, 0x6a)                              // i32.add
		body = inst.InstGlobalSet(body, heapGlobalIdx)         // heap_top = old + size
		return storeResult(body, op, ctx), nil
	}
	// Binary integer ops. Width comes from the op itself or
	// falls back to args' width (compare ops have i32 result but
	// the operand width determines whether to emit i32.eq vs
	// i64.eq).
	w := int8(32)
	if op.Width == 64 {
		w = 64
	} else if len(op.Args) > 0 && ctx.valueWidth[op.Args[0].ID] == 64 {
		w = 64
	}
	opcode, ok := binaryIntOpcode(op.Kind, w)
	if !ok {
		return nil, fmt.Errorf("wasmssa: unsupported op kind %v (width %d)", op.Kind, w)
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

// binaryIntOpcode returns the wasm opcode byte for a binary
// integer op kind at the given width (32 or 64). Returns
// (0, false) if the kind/width combo isn't supported.
func binaryIntOpcode(k ssa.OpKind, width int8) (byte, bool) {
	if width == 64 {
		switch k {
		case ssa.OpAdd:
			return 0x7c, true
		case ssa.OpSub:
			return 0x7d, true
		case ssa.OpMul:
			return 0x7e, true
		case ssa.OpDiv:
			return 0x7f, true // i64.div_s
		case ssa.OpDivU:
			return 0x80, true // i64.div_u
		case ssa.OpRem:
			return 0x81, true // i64.rem_s
		case ssa.OpRemU:
			return 0x82, true // i64.rem_u
		case ssa.OpAnd:
			return 0x83, true
		case ssa.OpOr:
			return 0x84, true
		case ssa.OpXor:
			return 0x85, true
		case ssa.OpShl:
			return 0x86, true
		case ssa.OpShr:
			return 0x87, true // i64.shr_s
		case ssa.OpShrU:
			return 0x88, true // i64.shr_u
		case ssa.OpEq:
			return 0x51, true
		case ssa.OpNe:
			return 0x52, true
		case ssa.OpLt:
			return 0x53, true // i64.lt_s
		case ssa.OpLtU:
			return 0x54, true
		case ssa.OpGt:
			return 0x55, true // i64.gt_s
		case ssa.OpGtU:
			return 0x56, true
		case ssa.OpLe:
			return 0x57, true // i64.le_s
		case ssa.OpLeU:
			return 0x58, true
		case ssa.OpGe:
			return 0x59, true // i64.ge_s
		case ssa.OpGeU:
			return 0x5a, true
		}
		return 0, false
	}
	return binaryI32Opcode(k)
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
