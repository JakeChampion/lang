package ssa

// Canonicalize rewrites Op operand positions to a normal
// form so structurally equivalent expressions hash to the
// same CSE key.
//
// Commutative binary ops get their Args ordered with the lower
// Value.ID first, which is what makes `a + b` and `b + a` look
// identical to CSE.
//
// Commutative ops:
//   - OpAdd, OpMul          (algebraic)
//   - OpAnd, OpOr, OpXor    (bitwise)
//   - OpEq, OpNe            (equality predicates)
//
// Phase 2 — directional comparisons (signed Lt/Le/Gt/Ge,
// unsigned LtU/LeU/GtU/GeU, ordered float FLt/FLe/FGt/FGe):
// when the LHS is a constant but the RHS isn't, swap operand
// positions and flip the comparison kind. `Lt(c, x)` becomes
// `Gt(x, c)`. This is semantically identical but moves the
// constant to the right where downstream CSE keys match
// `Gt(x, c)` from anywhere else in the function.
//
// Notably NOT commutative (operand swap would change
// semantics without a kind flip):
//   - OpSub, OpDiv, OpRem   (right-side identity differs)
//
// Ordering rule: by Value.ID ascending. Stable across
// re-runs; no dependency on dom tree or any other index.
//
// Pair with CSE for the canonical pipeline:
//
//	Fold(f)
//	Simplify(f)
//	Canonicalize(f)
//	CSE(f)
//
// (Optimize wires this in automatically, between Simplify and
// FoldBranches so const-folded args are sorted before CSE
// hashes them.)
func Canonicalize(f *Func) {
	if f == nil {
		return
	}
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if len(op.Args) != 2 {
				continue
			}
			if isCommutative(op.Kind) {
				if op.Args[0].ID > op.Args[1].ID {
					op.Args[0], op.Args[1] = op.Args[1], op.Args[0]
				}
				continue
			}
			// Directional comparison with a constant on the LHS:
			// swap operands and flip the predicate so the const
			// lands on the right.
			if flipped, ok := flipDirectionalCmp(op.Kind); ok {
				if isConstOp(op.Args[0], defs) && !isConstOp(op.Args[1], defs) {
					op.Kind = flipped
					op.Args[0], op.Args[1] = op.Args[1], op.Args[0]
				}
			}
		}
	}
}

// flipDirectionalCmp returns the equivalent comparison kind
// when operands are swapped. Lt ↔ Gt, Le ↔ Ge across signed,
// unsigned, and ordered-float variants. Returns (0, false)
// for kinds without a directional flip (Eq/Ne and the
// unordered-float predicates we don't have).
func flipDirectionalCmp(k OpKind) (OpKind, bool) {
	switch k {
	case OpLt:
		return OpGt, true
	case OpLe:
		return OpGe, true
	case OpGt:
		return OpLt, true
	case OpGe:
		return OpLe, true
	case OpLtU:
		return OpGtU, true
	case OpLeU:
		return OpGeU, true
	case OpGtU:
		return OpLtU, true
	case OpGeU:
		return OpLeU, true
	case OpFLt:
		return OpFGt, true
	case OpFLe:
		return OpFGe, true
	case OpFGt:
		return OpFLt, true
	case OpFGe:
		return OpFLe, true
	}
	return 0, false
}

// isConstOp reports whether `v` is defined by a const op.
func isConstOp(v Value, defs map[int32]*Op) bool {
	if !v.IsValid() {
		return false
	}
	def, ok := defs[v.ID]
	if !ok {
		return false
	}
	return IsConst(def.Kind)
}

func isCommutative(k OpKind) bool {
	switch k {
	case OpAdd, OpMul, OpAnd, OpOr, OpXor, OpEq, OpNe,
		OpFAdd, OpFMul, OpFEq, OpFNe:
		return true
	default:
		return false
	}
}
