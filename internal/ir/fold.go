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
// Returns whether any function's op list changed — the cleanup fixpoint
// (OptimizeCleanup) uses this to detect convergence without snapshotting +
// deep-comparing the whole program each iteration (#4377 slice 1b).
func Fold(prog *Program) bool {
	changed := false
	for _, fn := range prog.Funcs {
		for {
			next := foldOnce(fn.Ops)
			if opsEqual(next, fn.Ops) {
				break
			}
			fn.Ops = next
			changed = true
		}
	}
	return changed
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
		// Binary fold (i32): OpConstI32 a; OpConstI32 b; <binop>.
		if i+2 < len(ops) &&
			ops[i].Kind == OpConstI32 && ops[i+1].Kind == OpConstI32 &&
			isFoldableBinary(ops[i+2].Kind) {
			a, b := ops[i].I32, ops[i+1].I32
			res, ok := foldBinary(ops[i+2].Kind, ops[i+2].Unsigned, a, b)
			if ok {
				out = append(out, Op{Kind: OpConstI32, I32: res, Pos: ops[i+2].Pos})
				i += 2
				continue
			}
		}
		// Binary fold (i64): OpConstI64 a; OpConstI64 b; <binop>.
		// Comparison results stay as i32 (boolean-shaped). Arithmetic
		// and bitwise results stay as i64.
		if i+2 < len(ops) &&
			ops[i].Kind == OpConstI64 && ops[i+1].Kind == OpConstI64 &&
			isFoldableBinary(ops[i+2].Kind) {
			a, b := ops[i].I64, ops[i+1].I64
			if res, isCmp, ok := foldBinary64(ops[i+2].Kind, ops[i+2].Unsigned, a, b); ok {
				if isCmp {
					out = append(out, Op{Kind: OpConstI32, I32: int32(res), Pos: ops[i+2].Pos})
				} else {
					out = append(out, Op{Kind: OpConstI64, I64: res, Pos: ops[i+2].Pos})
				}
				i += 2
				continue
			}
		}
		// Width-conversion fold over constants:
		//   OpConstI32 a; OpExtendI32S  → OpConstI64 (int64(int32(a)))
		//   OpConstI32 a; OpExtendI32U  → OpConstI64 (int64(uint32(a)))
		//   OpConstI64 a; OpWrapI64     → OpConstI32 (int32(a))
		if i+1 < len(ops) && ops[i].Kind == OpConstI32 {
			switch ops[i+1].Kind {
			case OpExtendI32S:
				out = append(out, Op{Kind: OpConstI64, I64: int64(ops[i].I32), Pos: ops[i+1].Pos})
				i++
				continue
			case OpExtendI32U:
				out = append(out, Op{Kind: OpConstI64, I64: int64(uint32(ops[i].I32)), Pos: ops[i+1].Pos})
				i++
				continue
			}
		}
		if i+1 < len(ops) && ops[i].Kind == OpConstI64 && ops[i+1].Kind == OpWrapI64 {
			out = append(out, Op{Kind: OpConstI32, I32: int32(ops[i].I64), Pos: ops[i+1].Pos})
			i++
			continue
		}
		// Extend-then-wrap identity: any value extended to i64 and
		// immediately wrapped back to i32 is the original i32. This
		// shows up after auto-widening promotes one side of a mixed-
		// width binop only for the surrounding context to narrow
		// the result. Bridges signedness — extending then wrapping
		// preserves the low 32 bits regardless of which extend
		// variant ran.
		if i+1 < len(ops) && ops[i+1].Kind == OpWrapI64 &&
			(ops[i].Kind == OpExtendI32S || ops[i].Kind == OpExtendI32U) {
			i++ // skip both ops — leaves the underlying i32 producer in place
			continue
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
// returns the replacement op slice (the surviving arm wrapped in an
// OpBlock so any internal `br N` keeps the same relative depth) and
// the boolean true on success. On failure the caller falls through to
// the generic case.
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
// (or nothing, if there's no OpElse) survives. The result reuses the
// matched OpIf's blocktype on the wrapping OpBlock so an inner
// `br 0` that originally targeted the OpIf's End still lands on the
// same logical merge point and the wasm validator sees a stack-
// balanced block.
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
	if !armNeedsWrapping(arm) {
		return append([]Op{}, arm...), true
	}
	ifOp := ops[i+1]
	out := make([]Op, 0, len(arm)+2)
	out = append(out, Op{Kind: OpBlock, I32: ifOp.I32, Pos: ifOp.Pos})
	out = append(out, arm...)
	out = append(out, Op{Kind: OpEnd, Pos: ops[endIdx].Pos})
	return out, true
}

// armNeedsWrapping reports whether splicing arm in place of the
// enclosing OpIf would leave a stranded OpBr / OpBrIf whose depth
// targeted the if's own scope (or higher). Walking the arm with a
// local depth counter, any branch with `depth >= localDepth`
// targeted the if itself or some outer scope — removing the if
// would shift that depth by one and break wasm validation. The
// caller wraps the arm in an OpBlock with the if's blocktype to
// preserve the structure.
func armNeedsWrapping(arm []Op) bool {
	depth := 0
	for _, op := range arm {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			if depth > 0 {
				depth--
			}
		case OpBr, OpBrIf:
			if int(op.I32) >= depth {
				return true
			}
		}
	}
	return false
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
// interaction.
func isFoldableConst(k OpKind) bool {
	switch k {
	case OpConstI32, OpConstI64, OpConstF32, OpConstF64, OpConstStr, OpConstFunc:
		return true
	}
	return false
}

// isFoldableBinary reports whether a binary op produces a deterministic
// result from two constant inputs of matching width. Division and
// remainder are excluded because folding them would hide compile-
// time-detectable runtime traps (zero divisor) and silently change
// the program's observable behaviour. The Unsigned flag on the op
// is honoured by foldBinary / foldBinary64.
func isFoldableBinary(k OpKind) bool {
	switch k {
	case OpAdd, OpSub, OpMul,
		OpAnd, OpOr, OpXor,
		OpShl, OpShrS,
		OpEq, OpNe,
		OpLtS, OpLeS, OpGtS, OpGeS:
		return true
	}
	return false
}

// foldBinary computes the result of a fold-eligible binary op on two
// i32 constants. Shifts mask the count to 0..31 to match the wasm /
// arm semantics that codegen uses for runtime shifts. The unsigned
// flag flips OpShrS to a logical right shift and the order-comparison
// ops to their unsigned variants; signedness-agnostic ops (add, sub,
// mul, and/or/xor, eq, ne) ignore it. The bool result is reserved for
// ops that might bail out (none currently — DivS / RemS are excluded
// above).
func foldBinary(k OpKind, unsigned bool, a, b int32) (int32, bool) {
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
		if unsigned {
			return int32(uint32(a) >> (uint32(b) & 31)), true
		}
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
		var lt bool
		if unsigned {
			lt = uint32(a) < uint32(b)
		} else {
			lt = a < b
		}
		if lt {
			return 1, true
		}
		return 0, true
	case OpLeS:
		var le bool
		if unsigned {
			le = uint32(a) <= uint32(b)
		} else {
			le = a <= b
		}
		if le {
			return 1, true
		}
		return 0, true
	case OpGtS:
		var gt bool
		if unsigned {
			gt = uint32(a) > uint32(b)
		} else {
			gt = a > b
		}
		if gt {
			return 1, true
		}
		return 0, true
	case OpGeS:
		var ge bool
		if unsigned {
			ge = uint32(a) >= uint32(b)
		} else {
			ge = a >= b
		}
		if ge {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// foldBinary64 is the i64 counterpart to foldBinary. Returns the
// result, an isCmp flag indicating whether the result is a boolean
// (i32) rather than an i64, and an ok flag. Shifts mask the count to
// 0..63 to match wasm / arm i64 semantics.
func foldBinary64(k OpKind, unsigned bool, a, b int64) (int64, bool, bool) {
	switch k {
	case OpAdd:
		return a + b, false, true
	case OpSub:
		return a - b, false, true
	case OpMul:
		return a * b, false, true
	case OpAnd:
		return a & b, false, true
	case OpOr:
		return a | b, false, true
	case OpXor:
		return a ^ b, false, true
	case OpShl:
		return a << (uint64(b) & 63), false, true
	case OpShrS:
		if unsigned {
			return int64(uint64(a) >> (uint64(b) & 63)), false, true
		}
		return a >> (uint64(b) & 63), false, true
	case OpEq:
		if a == b {
			return 1, true, true
		}
		return 0, true, true
	case OpNe:
		if a != b {
			return 1, true, true
		}
		return 0, true, true
	case OpLtS:
		var lt bool
		if unsigned {
			lt = uint64(a) < uint64(b)
		} else {
			lt = a < b
		}
		if lt {
			return 1, true, true
		}
		return 0, true, true
	case OpLeS:
		var le bool
		if unsigned {
			le = uint64(a) <= uint64(b)
		} else {
			le = a <= b
		}
		if le {
			return 1, true, true
		}
		return 0, true, true
	case OpGtS:
		var gt bool
		if unsigned {
			gt = uint64(a) > uint64(b)
		} else {
			gt = a > b
		}
		if gt {
			return 1, true, true
		}
		return 0, true, true
	case OpGeS:
		var ge bool
		if unsigned {
			ge = uint64(a) >= uint64(b)
		} else {
			ge = a >= b
		}
		if ge {
			return 1, true, true
		}
		return 0, true, true
	}
	return 0, false, false
}

// opsEqual is the slice-equality used to detect a fixed point. Compares
// every field that participates in folding decisions; positions are
// included so a no-op rewrite that only moves a Pos is treated as
// changed (an unlikely edge but it keeps the contract clean).
//
// Op contains slice (ArgTypes) and pointer (Sig) fields so it's no
// longer struct-comparable with `==`; opEqual walks fields explicitly.
func opsEqual(a, b []Op) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !opEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// opEqual reports field-wise equality for two Op values. ArgTypes
// is compared length-only — the fold pass never rewrites ArgTypes
// on the same op kind (it threads them through unchanged via
// shallow copy), so a length match is sufficient to detect a
// fixed point. Direct value-equality on ast.Type would panic
// because composite types (StructType, EnumType) contain
// uncomparable slice / map fields.
func opEqual(a, b Op) bool {
	if a.Kind != b.Kind || a.I32 != b.I32 || a.I64 != b.I64 ||
		a.F32 != b.F32 || a.F64 != b.F64 || a.Width != b.Width ||
		a.Unsigned != b.Unsigned || a.Str != b.Str ||
		a.Sig() != b.Sig() || a.Str2() != b.Str2() || a.Pos != b.Pos {
		return false
	}
	return len(a.ArgTypes()) == len(b.ArgTypes())
}
