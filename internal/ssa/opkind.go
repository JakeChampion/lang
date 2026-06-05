package ssa

// Op classification predicates. Centralised here so downstream
// passes (CSE, custom user analyses, the upcoming IR→SSA lift)
// stay in sync as the OpKind enum grows.

// IsCommutative reports whether `k` is a binary commutative
// op — `a op b == b op a`. Used by Canonicalize to sort
// operand positions, and by CSE to recognise structurally
// equivalent expressions whose args differ in order.
func IsCommutative(k OpKind) bool {
	return isCommutative(k)
}

// IsPure reports whether `k` has no observable side effect
// beyond producing its Result Value. Pure ops can be:
//   - deleted when their Result has no consumers (DCE);
//   - deduplicated when their (Kind, Imm, Str, Args) tuple
//     matches another op (CSE);
//   - hoisted out of loops (future LICM pass).
//
// Non-pure ops: Call (might do anything), Load (re-reads
// possibly-mutated memory), Store (writes memory).
//
// Phi is pure in the strict sense (no memory effect) but
// callers usually want to exclude it from CSE / DCE moves
// because its meaning depends on Block + Preds order. Check
// `k == OpPhi` separately when that matters.
func IsPure(k OpKind) bool {
	switch k {
	case OpCall, OpCallIndirect, OpCallPair,
		OpLoad, OpStore,
		OpLoad8S, OpLoad8U, OpLoad16S, OpLoad16U,
		OpStore8, OpStore16,
		OpLoadF, OpStoreF,
		OpAlloc,
		OpMakeClosure, OpMakeEnv:
		return false
	default:
		return true
	}
}

// MayTrap reports whether an op can fault at runtime independent of
// whether its result is consumed. Today that is the integer
// divide/remainder ops: they trap on a zero divisor (wasm i32.div_s
// traps; arm64/x86 raise SIGFPE/#DE). Such an op must not be deleted by
// DCE or hoisted by LICM even when its result is dead, or the program's
// observable trap behavior changes. IsPure can still report these as
// pure for CSE (deduplicating two identical divisions is safe).
// See docs/ADVERSARIAL-REVIEW-2026-06.md (I2).
func MayTrap(k OpKind) bool {
	switch k {
	case OpDiv, OpDivU, OpRem, OpRemU:
		return true
	}
	return false
}

// IsConst reports whether `k` produces a compile-time
// constant Value (and so participates in fold/strength
// reduction lookups).
func IsConst(k OpKind) bool {
	switch k {
	case OpConstInt, OpConstBool, OpConstString, OpConstFloat:
		return true
	default:
		return false
	}
}

// IsComparison reports whether `k` is a binary predicate
// producing a boolean Value (Eq / Ne / Lt / Le / Gt / Ge,
// across signed, unsigned, and float variants). Used by
// CmpFlip to recognise the not(cmp) rewrite shape and by the
// upcoming branch-on-comparison patterns.
//
// To distinguish integer-only from float, callers can pair
// with the per-kind class table — integer cmps fold to
// const_bool under integer-self identities (`x == x → true`)
// but float cmps don't (NaN ≠ NaN).
func IsComparison(k OpKind) bool {
	switch k {
	case OpEq, OpNe,
		OpLt, OpLtU, OpLe, OpLeU,
		OpGt, OpGtU, OpGe, OpGeU,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		return true
	default:
		return false
	}
}
