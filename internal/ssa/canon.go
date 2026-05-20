package ssa

// Canonicalize rewrites Op operand positions to a normal
// form so structurally equivalent expressions hash to the
// same CSE key.
//
// Phase 1 covers commutative binary ops: Args ordered with
// the lower Value.ID first. This is enough to make
// `a + b` and `b + a` look identical to CSE.
//
// Commutative ops:
//   - OpAdd, OpMul          (algebraic)
//   - OpEq, OpNe            (equality predicates)
//
// Notably NOT commutative (operand swap would change
// semantics):
//   - OpSub, OpDiv, OpRem   (right-side identity differs)
//   - OpLt, OpLe, OpGt, OpGe (would invert the comparison)
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
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if !isCommutative(op.Kind) {
				continue
			}
			if len(op.Args) != 2 {
				continue
			}
			if op.Args[0].ID > op.Args[1].ID {
				op.Args[0], op.Args[1] = op.Args[1], op.Args[0]
			}
		}
	}
}

func isCommutative(k OpKind) bool {
	switch k {
	case OpAdd, OpMul, OpEq, OpNe:
		return true
	default:
		return false
	}
}
