// Constant folding on the lowered IR.
//
// The AST-level optimiser folds literal arithmetic before lowering, so
// most simple cases (`1 + 2`, `(3 < 5) && true`) are already i32
// constants by the time the IR is built. The IR pass exists for the
// shapes only visible *after* lowering:
//
//   - ternary and short-circuit `&&` / `||` decompose to OpIf — when
//     the condition lowers to a constant, the surviving arm can be
//     spliced into the body and the dead arm dropped wholesale;
//   - inlining (when added) splices entire bodies into the caller's
//     op list, exposing new constant chains;
//   - closure conversion can turn a captured-variable reference into
//     an env-relative load even when the source is a static constant
//     — a future capture-elimination pass would lean on the same
//     primitives.
//
// Two transforms run to a fixed point:
//
//  1. Linear arithmetic / comparison folding: a window of
//     `OpConstI32 a; OpConstI32 b; <binop>` collapses to
//     `OpConstI32 (a OP b)`; `OpConstI32 a; OpNot` collapses to
//     `OpConstI32 (a == 0 ? 1 : 0)`.
//  2. Constant-if pruning: if an OpIf is preceded by a constant
//     condition, the entire `OpIf … OpElse … OpEnd` block is replaced
//     with the surviving arm's ops.
//
// Division and remainder are deliberately skipped: folding `1 / 0` at
// compile time would silently swallow a runtime trap. Floats stay
// untouched too — the AST optimiser handles them and there's no
// portable way to round-trip every f32 bit-pattern through the
// IR's float ops without surprising the user.

package ir

// Fold rewrites every function in prog to its constant-folded form.
// Iterates each function to a fixed point (constant arithmetic can
// cascade — `1 + 2 + 3` lowers to two adds, the first fold reveals
// the second), so backends never need to re-run the pass.
func Fold(prog *Program) {
	for _, fn := range prog.Funcs {
		for {
			next := foldOnce(fn.Ops)
			if opsEqual(next, fn.Ops) {
				break
			}
			fn.Ops = next
		}
	}
}

// foldOnce runs one pass over ops, applying every recognised pattern.
// Recursion is replaced with re-iteration in Fold to keep the helper
// small and easy to reason about.
func foldOnce(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		// Constant-if pruning: const-cond + OpIf … OpEnd block.
		if i+1 < len(ops) && ops[i].Kind == OpConstI32 && ops[i+1].Kind == OpIf {
			if newOps, ok := pruneConstIf(ops, i); ok {
				out = append(out, newOps...)
				i = endOfIfBlock(ops, i+1)
				continue
			}
		}
		// Binary fold: OpConstI32 a; OpConstI32 b; <binop>.
		if i+2 < len(ops) &&
			ops[i].Kind == OpConstI32 && ops[i+1].Kind == OpConstI32 &&
			isFoldableBinary(ops[i+2].Kind) {
			a, b := ops[i].I32, ops[i+1].I32
			res, ok := foldBinary(ops[i+2].Kind, a, b)
			if ok {
				out = append(out, Op{Kind: OpConstI32, I32: res, Pos: ops[i+2].Pos})
				i += 2
				continue
			}
		}
		// const ; drop pair → remove both. ConstPropagate plus the
		// dead-store rewrite in PropagateCopies leave behind
		// `const X ; OpDrop` chains; collapse them so the operand
		// stack stays balanced without the noise.
		if i+1 < len(ops) && isFoldableConst(ops[i].Kind) && ops[i+1].Kind == OpDrop {
			i++ // also consume the OpDrop
			continue
		}
		// Unary fold: OpConstI32 a; OpNot.
		if i+1 < len(ops) && ops[i].Kind == OpConstI32 && ops[i+1].Kind == OpNot {
			a := ops[i].I32
			res := int32(0)
			if a == 0 {
				res = 1
			}
			out = append(out, Op{Kind: OpConstI32, I32: res, Pos: ops[i+1].Pos})
			i++
			continue
		}
		out = append(out, ops[i])
	}
	return out
}

// pruneConstIf rewrites a constant-conditioned if-block. ops[i] is the
// OpConstI32 condition; ops[i+1] is the matched OpIf. The function
// returns the replacement op slice (the surviving arm's body) and the
// boolean true on success. On failure the caller falls through to the
// generic case.
//
// The if-block layout in IR:
//
//	(i)    OpConstI32 cond
//	(i+1)  OpIf       blocktype
//	         <then arm ops>
//	(j)    OpElse                 ; optional
//	         <else arm ops>
//	(k)    OpEnd
//
// On a non-zero condition the then-arm survives; on zero the else-arm
// (or nothing, if there's no OpElse) survives.
func pruneConstIf(ops []Op, i int) ([]Op, bool) {
	thenStart := i + 2
	elseIdx, endIdx := scanIfBlock(ops, i+1)
	if endIdx < 0 {
		return nil, false
	}
	cond := ops[i].I32
	var arm []Op
	if cond != 0 {
		thenEnd := elseIdx
		if thenEnd < 0 {
			thenEnd = endIdx
		}
		arm = ops[thenStart:thenEnd]
	} else if elseIdx >= 0 {
		arm = ops[elseIdx+1 : endIdx]
	}
	return append([]Op{}, arm...), true
}

// scanIfBlock locates the OpElse (if any) and OpEnd matching the OpIf
// at ops[ifIdx]. Returns -1 for elseIdx when the if has no else, and
// -1 / -1 if the block is malformed (no matching OpEnd at depth 0).
//
// Tracks nested block / loop / if scopes so we don't wrongly match an
// inner OpElse / OpEnd with the outer OpIf.
func scanIfBlock(ops []Op, ifIdx int) (elseIdx, endIdx int) {
	depth := 1
	elseIdx = -1
	for j := ifIdx + 1; j < len(ops); j++ {
		switch ops[j].Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpElse:
			if depth == 1 && elseIdx < 0 {
				elseIdx = j
			}
		case OpEnd:
			depth--
			if depth == 0 {
				return elseIdx, j
			}
		}
	}
	return -1, -1
}

// endOfIfBlock returns the op index immediately past the OpEnd of the
// if-block that opened at ifIdx. It's the cursor advance the outer
// loop in foldOnce uses after splicing in the surviving arm.
func endOfIfBlock(ops []Op, ifIdx int) int {
	_, end := scanIfBlock(ops, ifIdx)
	return end // outer `i++` from the for loop steps past the OpEnd
}

// isFoldableConst reports whether a const op carries a value that's
// safe to drop wholesale — no side effects, no allocator
// interaction. All four IR const ops qualify.
func isFoldableConst(k OpKind) bool {
	switch k {
	case OpConstI32, OpConstF32, OpConstStr, OpConstFunc:
		return true
	}
	return false
}

// isFoldableBinary reports whether a binary op produces a deterministic
// i32 from two i32 inputs. Division and remainder are excluded because
// folding them would hide compile-time-detectable runtime traps
// (zero divisor) and silently change the program's observable
// behaviour.
func isFoldableBinary(k OpKind) bool {
	switch k {
	case OpAdd, OpSub, OpMul,
		OpAnd, OpOr, OpXor,
		OpShl, OpShrS,
		OpEq, OpNe, OpLtS, OpLeS, OpGtS, OpGeS:
		return true
	}
	return false
}

// foldBinary computes the result of a fold-eligible binary op on two
// i32 constants. Shifts mask the count to 0..31 to match the wasm /
// arm semantics that codegen uses for runtime shifts. The bool result
// is reserved for ops that might bail out (none currently — DivS /
// RemS are excluded above).
func foldBinary(k OpKind, a, b int32) (int32, bool) {
	switch k {
	case OpAdd:
		return a + b, true
	case OpSub:
		return a - b, true
	case OpMul:
		return a * b, true
	case OpAnd:
		return a & b, true
	case OpOr:
		return a | b, true
	case OpXor:
		return a ^ b, true
	case OpShl:
		return a << (uint32(b) & 31), true
	case OpShrS:
		return a >> (uint32(b) & 31), true
	case OpEq:
		if a == b {
			return 1, true
		}
		return 0, true
	case OpNe:
		if a != b {
			return 1, true
		}
		return 0, true
	case OpLtS:
		if a < b {
			return 1, true
		}
		return 0, true
	case OpLeS:
		if a <= b {
			return 1, true
		}
		return 0, true
	case OpGtS:
		if a > b {
			return 1, true
		}
		return 0, true
	case OpGeS:
		if a >= b {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// opsEqual is the slice-equality used to detect a fixed point. Compares
// every field that participates in folding decisions; positions are
// included so a no-op rewrite that only moves a Pos is treated as
// changed (an unlikely edge but it keeps the contract clean).
func opsEqual(a, b []Op) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
