package ssa

// TrivialPhis aliases phi-Op results to their single distinct
// non-self argument when one exists, then rewrites uses
// elsewhere in the function to point at that argument
// directly. Pair with DCE to reclaim the now-orphan phi Op.
//
// Cases handled:
//
//   - `phi v` (single-arg phi after FoldBranches dropped the
//     other inbound edges) → `v`.
//   - `phi v, v, …, v` (all incoming values identical) → `v`.
//   - `phi v, phi-result, v, phi-result, v` (self-references
//     allowed; the iterative phi-cycle in a loop header
//     usually has the self-ref as one arg). Treated as
//     trivial when, ignoring self-refs, only one distinct
//     Value remains.
//
// Phis whose surviving args span ≥2 distinct non-self Values
// are not trivial and stay.
//
// Single-pass: the substitution map is built once by walking
// every phi, then applySubstitutions resolves chains
// transitively (so a phi feeding another phi that's also
// trivial collapses in one shot). The phi Ops themselves
// stay in the IR after rewriting; pair with DCE to reclaim
// them now that their results have no consumers.
func TrivialPhis(f *Func) {
	if f == nil {
		return
	}
	sub := map[int32]Value{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind != OpPhi {
				continue
			}
			if !op.Result.IsValid() || len(op.Args) == 0 {
				continue
			}
			surviving, ok := trivialPhiTarget(op)
			if !ok {
				continue
			}
			sub[op.Result.ID] = surviving
		}
	}
	if len(sub) == 0 {
		return
	}
	applySubstitutions(f, sub)
}

// trivialPhiTarget reports whether `op` (assumed OpPhi) is
// trivial — i.e. it has at most one distinct non-self-ref
// argument — and, if so, returns that Value.
func trivialPhiTarget(op *Op) (Value, bool) {
	var first Value
	for _, a := range op.Args {
		if !a.IsValid() {
			return Value{}, false
		}
		if a == op.Result {
			// self-reference; allowed but doesn't count toward "distinct".
			continue
		}
		if !first.IsValid() {
			first = a
			continue
		}
		if a != first {
			return Value{}, false
		}
	}
	if !first.IsValid() {
		// All args were self-refs (degenerate but legal). Pick
		// the result itself — but that would create an infinite
		// substitution. Bail out instead; DCE will not drop a
		// self-referencing phi, which is fine.
		return Value{}, false
	}
	return first, true
}
