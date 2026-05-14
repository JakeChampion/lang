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
	for _, fn := range prog.Funcs {
		fn.Ops = flattenOps(fn.Ops, fn.ReturnType, ptrW)
	}
}

func flattenOps(ops []Op, retType ast.Type, ptrW int) []Op {
	out := make([]Op, 0, len(ops))
	depth := int32(0)
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		// Only consider OpIf at function-root depth (no surrounding
		// scope), with no OpElse, and a then-arm that returns.
		if depth == 0 && op.Kind == OpIf && op.I32 == BlockTypeVoid {
			if newOps, advance, ok := tryFlattenIf(ops, i, retType, ptrW); ok {
				out = append(out, newOps...)
				i += advance - 1 // outer loop's i++ advances by one more
				continue
			}
		}
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
		}
		out = append(out, op)
	}
	return out
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

// returnBlockType maps the function's declared return type to the
// matching block result type. Used by flatten + the multi-return
// inliner; kept as a small helper next to the blocktype-emitting
// passes. i64 / f64 / f32 surface their wider block types so the
// wrapper's signature matches the wat side.
func returnBlockType(t ast.Type) int32 {
	return returnBlockTypeFor(t, 4)
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
