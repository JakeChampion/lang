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
	case OpCall, OpCallIndirect, OpLoad, OpStore:
		return false
	default:
		return true
	}
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
// producing a boolean Value (Eq / Ne / Lt / Le / Gt / Ge).
// Used by CmpFlip to recognise the not(cmp) rewrite shape
// and by the upcoming branch-on-comparison patterns.
func IsComparison(k OpKind) bool {
	switch k {
	case OpEq, OpNe,
		OpLt, OpLtU, OpLe, OpLeU,
		OpGt, OpGtU, OpGe, OpGeU:
		return true
	default:
		return false
	}
}
