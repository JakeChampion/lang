// Branch flattening: turn `if c { return X; } return Y;` into a
// single value-returning if.
//
// The lowering pass emits an `if c { return X; }` shape as
//
//	<cond>
//	OpIf void
//	  <X-ops>
//	  OpReturn
//	OpEnd
//
// followed by whatever comes after — typically the function's
// trailing `return Y;`. Both arms of the conditional reach a
// return; one inside the if's then-arm, one after the if-end. From
// the source's POV they're really the two branches of one decision,
// but the IR carries them as a void-result if plus separate
// continuation. Flattening merges them:
//
//	<cond>
//	OpIf <return-type>
//	  <X-ops without trailing OpReturn>
//	OpElse
//	  <continuation ops without trailing OpReturn>
//	OpEnd
//	OpReturn
//
// Two payoffs:
//
//   - One OpReturn instead of two; backends emit one fewer branch
//     to the epilogue.
//   - The if becomes a typed value-returning conditional, which
//     ir.Fold can prune if the condition turns out to be a known
//     constant after const propagation.
//
// Eligibility (deliberately conservative for the first pass):
//
//   - the OpIf sits at function-root depth (no surrounding open
//     scopes); the rewrite reasons about absolute depths so a
//     nested OpIf would need extra arithmetic that's not worth
//     it yet,
//   - the if has NO OpElse,
//   - the then-arm's last op (right before OpEnd) is OpReturn or
//     OpReturnVoid,
//   - the continuation between the OpEnd and the matching
//     trailing OpReturn contains no control-flow ops (block /
//     loop / if / br / br_if). Continuations with control flow
//     can be flattened too in principle but it'd take a relooper-
//     style analysis to keep depths consistent across the rewrite.
//   - the two return ops are the same kind — both OpReturn or both
//     OpReturnVoid. Mixed kinds would mean the function declared
//     a non-void return type but one path leaks through with no
//     value; that's a checker-rejected program.
//
// More general patterns (multi-arm, nested) are deferred. The
// first-pass shape is by far the most common one in practice
// (early-bailout helpers, guard-then-default returns).

package ir

import "github.com/jakechampion/lang/internal/ast"

// FlattenBranches rewrites every eligible `if-then-return / return`
// pair in prog as a typed if/else with a single trailing return.
// Programs without such a pair are unchanged.
func FlattenBranches(prog *Program) {
	ptrW := prog.PtrW
	if ptrW == 0 {
		ptrW = 4
	}
	sigs := buildFuncSigs(prog)
	for _, fn := range prog.Funcs {
		fn.Ops = flattenOps(fn.Ops, fn.ReturnType, ptrW, sigs)
	}
}

func flattenOps(ops []Op, retType ast.Type, ptrW int, sigs map[string]funcSig) []Op {
	out := make([]Op, 0, len(ops))
	depth := int32(0)
	// dataDepth tracks the operand stack depth at the current
	// linear position, simulated forward from the function start.
	// Flatten is only sound when the OpIf is at "statement
	// position" — i.e., the operand stack right before the if
	// holds only the just-pushed condition. Otherwise the
	// rewrite swallows the outer-stack values into a block that
	// can't reach them, producing a wasm validator error
	// ("type mismatch: expected i32 but nothing on stack").
	// `dataDepthValid` flips to false on the first op with
	// unknown stack effect (typically a call we couldn't resolve);
	// from that point on the rest of the function skips flatten
	// — conservative, but soundness-preserving.
	dataDepth := 0
	dataDepthValid := true
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		// Only consider OpIf at function-root depth (no surrounding
		// scope), with no OpElse, and a then-arm that returns.
		if depth == 0 && op.Kind == OpIf && op.I32 == BlockTypeVoid && dataDepthValid && dataDepth == 1 {
			if newOps, advance, ok := tryFlattenIf(ops, i, retType, ptrW); ok {
				out = append(out, newOps...)
				i += advance - 1 // outer loop's i++ advances by one more
				// Post-flatten the rewritten region ends with a
				// single OpReturn / OpReturnVoid that takes the
				// function out — nothing past `advance` is reached
				// in straight-line flow, but the outer scan keeps
				// going (other top-level ifs in the same function
				// can still flatten). Reset the data stack
				// tracker: after the trailing return everything
				// downstream is either unreachable or starts
				// fresh from a depth-0 baseline.
				dataDepth = 0
				dataDepthValid = true
				continue
			}
		}
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
		}
		if dataDepthValid {
			d, ok := simulateOpEffect(op, dataDepth, sigs)
			if !ok {
				dataDepthValid = false
			} else {
				dataDepth = d
			}
		}
		out = append(out, op)
	}
	return out
}

// funcSig captures the operand-stack effect of calling a function:
// `pops` is the number of argument values popped (one per param,
// plus an extra i32 for OpCallIndirect's table index — handled at
// the call site, not stored here), `pushes` is the result count
// (0 for void returns, 1 for scalar returns, 2 for pair-form or
// two-word-string returns). Pair-form is special-cased on read
// because the program's `PairForm` map drives the result count;
// the funcSig itself records the scalar-return baseline.
type funcSig struct {
	params int
	// resultCount: 0 if ReturnType is VoidType, otherwise 1.
	// Pair-form callees push 2; the caller adjusts via PairForm.
	resultCount int
	pairForm    bool
	// stringResult: two-word string returns push 2 on wasm32
	// (data + len). The PtrW is folded in at build time so the
	// caller doesn't need to re-derive it.
	stringResult bool
}

// buildFuncSigs maps every function name to its arg / result
// count so simulateOpEffect can resolve OpCallDirect's stack
// delta. Closures get the same treatment via the hoisted target
// name; OpCallIndirect references aren't reflected here (the call
// op itself encodes its param count via op.I32).
func buildFuncSigs(prog *Program) map[string]funcSig {
	out := make(map[string]funcSig, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		sig := funcSig{
			params:   len(fn.Params),
			pairForm: prog.PairForm[fn.Name],
		}
		if fn.ReturnType != nil {
			if _, isVoid := fn.ReturnType.(ast.VoidType); !isVoid {
				sig.resultCount = 1
				if _, isStr := fn.ReturnType.(ast.StringType); isStr && prog.PtrW == 4 {
					sig.stringResult = true
				}
			}
		}
		out[fn.Name] = sig
	}
	return out
}

// simulateOpEffect updates dataDepth for `op` and reports whether
// the effect is known. Returns (newDepth, true) on success;
// (0, false) when the op's stack effect can't be determined (an
// unresolved call, a closure call with unknown signature, etc.) —
// callers treat that as "stop tracking" and skip further flattens
// in this function.
//
// Control-flow ops (OpBlock/OpLoop/OpIf/OpElse/OpEnd/OpBr/OpBrIf)
// modify the scope stack as well as the data stack; this helper
// only touches the data stack because the caller already tracks
// control-flow depth separately (`depth`) and the flatten gate
// fires only at depth==0.
func simulateOpEffect(op Op, dataDepth int, sigs map[string]funcSig) (int, bool) {
	pops, pushes, ok := opStackEffect(op, sigs)
	if !ok {
		return 0, false
	}
	d := dataDepth - pops
	if d < 0 {
		// Function-internal underflow — the op consumes more
		// than the simulated stack holds. Means our tracker is
		// off (signature mismatch, or an op we modeled wrong);
		// stop tracking to avoid false flattens.
		return 0, false
	}
	d += pushes
	return d, true
}

// opStackEffect returns (pops, pushes, ok) for `op` outside of
// control-flow tracking. Ops with branches / labels return
// ok=false — the caller skips data-stack updates past them. The
// switch covers every op kind the IR emits; unrecognised kinds
// fall through to ok=false (defensive — adding a new op shouldn't
// silently miscount).
func opStackEffect(op Op, sigs map[string]funcSig) (pops int, pushes int, ok bool) {
	switch op.Kind {
	// Zero-effect source-line marker (native -g only): no stack effect.
	case OpLine:
		return 0, 0, true
	// Constants — produce one value.
	case OpConstI32, OpConstI64, OpConstF32, OpConstF64, OpConstStr, OpConstFunc:
		return 0, 1, true
	// One-in, one-out conversions / unary.
	case OpExtendI32S, OpExtendI32U, OpWrapI64, OpFPromoteF32, OpFDemoteF64,
		OpFConvertI32, OpFConvertI64, OpITruncF32, OpITruncF64,
		OpReinterpretI32F32, OpReinterpretF32I32,
		OpReinterpretI64F64, OpReinterpretF64I64,
		OpNot, OpFNeg,
		OpClz, OpCtz, OpPopcount:
		return 1, 1, true
	// Locals.
	case OpLoadLocal:
		// Two-word string reads push (data, len) — same shape
		// as a heap pointer + extra len word — but the IR
		// surfaces that via WidthString. Default reads push 1.
		if op.Width == WidthString {
			return 0, 2, true
		}
		return 0, 1, true
	case OpStoreLocal:
		if op.Width == WidthString {
			return 2, 0, true
		}
		return 1, 0, true
	case OpTeeLocal:
		if op.Width == WidthString {
			return 2, 2, true
		}
		return 1, 1, true
	// Binary arithmetic / comparison.
	case OpAdd, OpSub, OpMul, OpDivS, OpRemS, OpAnd, OpOr, OpXor, OpShl, OpShrS,
		OpEq, OpNe, OpLtS, OpLeS, OpGtS, OpGeS,
		OpFAdd, OpFSub, OpFMul, OpFDiv,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		return 2, 1, true
	// Memory.
	case OpLoad, OpLoadByte, OpFLoad:
		// String / two-word loads expand to (data, len); the
		// IR records that via Width=WidthString. Plain pointer
		// loads stay 1→1 even when Width=WidthPtr because both
		// arm64 and wasm read a single pointer-sized value.
		if op.Width == WidthString {
			return 1, 2, true
		}
		return 1, 1, true
	case OpStore, OpFStore, OpStoreI8:
		if op.Width == WidthString {
			return 3, 0, true
		}
		return 2, 0, true
	case OpAlloc:
		return 1, 1, true
	case OpStrEq, OpStrCmp, OpStrConcat:
		// Inputs are two two-word string pairs (4 i32s); outputs
		// depend on op. OpStrEq returns 1 (boolean) and OpStrCmp 1
		// (the three-way i32); OpStrConcat returns a new (data, len)
		// pair via multi-value wasm.
		if op.Kind == OpStrConcat {
			return 4, 2, true
		}
		return 4, 1, true
	case OpStrLen:
		return 2, 1, true
	case OpEnumSentinel:
		return 0, 1, true
	case OpMatchTag:
		return 1, 1, true
	case OpDrop:
		// Width=WidthString drops two i32 slots (data + len);
		// every other Drop kind pops a single value.
		if op.Width == WidthString {
			return 2, 0, true
		}
		return 1, 0, true
	// Pair-return makers.
	case OpMakeSomeI32, OpMakeOkI32, OpMakeErrI32:
		return 1, 2, true
	case OpMakeNoneI32:
		return 0, 2, true
	// Returns + traps end straight-line flow; for the purpose of
	// the forward simulator we consume the values they read off
	// the stack. The caller's control-flow tracker already
	// reflects the unreachability, but we keep ok=true so the
	// data tracker advances past these without giving up.
	case OpReturn:
		return 1, 0, true
	case OpReturnVoid:
		return 0, 0, true
	case OpReturnPair:
		return 2, 0, true
	// Dedicated rc ops (#4402 opt 2): pass-through shaped, so the
	// exact effect is (1, 1). Deliberately ok=false for now —
	// their OpCallDirect ancestors bailed here too (the rc helpers
	// aren't in the funcSig table), and keeping the bail preserves
	// byte-identical flatten decisions. Opt 2b may flip this to
	// `return 1, 1, true` once the change is measured on its own.
	case OpRcInc, OpRcDec, OpRcIsUnique:
		return 0, 0, false
	// Calls: resolve via the funcSig table when possible.
	case OpCallDirect, OpCallDirectPair:
		sig, ok := sigs[op.Str]
		if !ok {
			return 0, 0, false
		}
		// Pair-form result count: callers consume (tag, payload)
		// when OpCallDirectPair, or 1 when OpCallDirect (heap-
		// box rebox).
		pushes := sig.resultCount
		if op.Kind == OpCallDirectPair {
			pushes = 2
		} else if sig.stringResult {
			pushes = 2
		}
		return sig.params, pushes, true
	case OpCallIndirect:
		// op.I32 is the arg count (without the table-index
		// operand). The result count isn't recorded on the op —
		// stop tracking past an indirect call rather than
		// guess.
		return 0, 0, false
	case OpCallClosureDirect:
		// Closure call: params + env_ptr operand. The result
		// count needs the callee's signature, but
		// OpCallClosureDirect's callee is identified by op.Str
		// (the hoisted target name).
		sig, ok := sigs[op.Str]
		if !ok {
			return 0, 0, false
		}
		// +1 pop for the env pointer the IR appends as the
		// last operand.
		pops := sig.params
		if pops < 1 {
			pops = 1
		}
		pushes := sig.resultCount
		if sig.stringResult {
			pushes = 2
		}
		return pops, pushes, true
	case OpMakeClosure, OpMakeEnv:
		// op.I32 carries the capture count. Closures push one
		// pointer-sized value (the closure ptr).
		return int(op.I32), 1, true
	// Control-flow ops: handled outside the data-stack tracker
	// (we surrender data tracking once we hit one mid-stream
	// since the relooper-style depth math doesn't compose with
	// the simple linear walk used here). The depth==0 gate
	// upstream means we only fire on top-level patterns; nested
	// scopes naturally bail.
	case OpBlock, OpLoop, OpIf, OpElse, OpEnd, OpBr, OpBrIf:
		return 0, 0, false
	}
	return 0, 0, false
}

// tryFlattenIf checks whether the OpIf at ops[i] satisfies the
// eligibility rules. On success it returns the rewritten op slice
// (just the new <if T> ... <else> ... <end> <return> region — the
// caller appends it to the output stream) and the number of input
// ops to advance past (the original if-block plus its
// continuation up to and including the trailing return).
func tryFlattenIf(ops []Op, ifIdx int, retType ast.Type, ptrW int) ([]Op, int, bool) {
	elseIdx, endIdx := scanIfBlock(ops, ifIdx)
	if elseIdx >= 0 || endIdx < 0 {
		// The if has an explicit else, or the IR is malformed.
		return nil, 0, false
	}
	// Then-arm last op (just before OpEnd) must be a return.
	thenLastIdx := endIdx - 1
	if thenLastIdx <= ifIdx {
		return nil, 0, false
	}
	thenLast := ops[thenLastIdx]
	if thenLast.Kind != OpReturn && thenLast.Kind != OpReturnVoid {
		return nil, 0, false
	}
	// Continuation runs from endIdx+1 until a matching trailing
	// return at depth 0 — bail on any nested control flow.
	contStart := endIdx + 1
	contRetIdx := -1
	for j := contStart; j < len(ops); j++ {
		switch ops[j].Kind {
		case OpBlock, OpLoop, OpIf:
			return nil, 0, false
		case OpReturn, OpReturnVoid:
			contRetIdx = j
		}
		if contRetIdx >= 0 {
			break
		}
	}
	if contRetIdx < 0 || ops[contRetIdx].Kind != thenLast.Kind {
		return nil, 0, false
	}

	bt := returnBlockTypeFor(retType, ptrW)
	out := make([]Op, 0, (contRetIdx-ifIdx)+4)
	out = append(out, Op{Kind: OpIf, I32: bt, Pos: ops[ifIdx].Pos})
	// Then-arm body: skip the original OpIf header AND the
	// trailing OpReturn before OpEnd.
	for k := ifIdx + 1; k < thenLastIdx; k++ {
		out = append(out, ops[k])
	}
	out = append(out, Op{Kind: OpElse})
	// Else-arm body: the continuation, dropping the trailing
	// return that we'll re-emit once after OpEnd.
	for k := contStart; k < contRetIdx; k++ {
		out = append(out, ops[k])
	}
	out = append(out, Op{Kind: OpEnd})
	out = append(out, Op{Kind: thenLast.Kind, Pos: ops[contRetIdx].Pos})
	return out, contRetIdx - ifIdx + 1, true
}

// returnBlockTypeFor is the ptrW-aware variant. On wasm32
// (ptrW=4) string-typed returns surface as `BlockTypeStringPair`
// so the inliner's wrapper block / function-result clause matches
// the two-word ABI's `(result i32 i32)` shape. Natives stay on
// `BlockTypeI32` (one pointer slot under their existing LSB-
// tagged SSO).
func returnBlockTypeFor(t ast.Type, ptrW int) int32 {
	if t == nil {
		return BlockTypeVoid
	}
	if _, ok := t.(ast.VoidType); ok {
		return BlockTypeVoid
	}
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return BlockTypeI64
	}
	if f, ok := t.(ast.FloatType); ok {
		if f.Width == 64 {
			return BlockTypeF64
		}
		return BlockTypeF32
	}
	if _, ok := t.(ast.StringType); ok && ptrW == 4 {
		return BlockTypeStringPair
	}
	return BlockTypeI32
}
