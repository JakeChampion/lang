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
//   - `phi c1, c2` where c1 and c2 are distinct const Ops with
//     identical immediate (e.g. two separate `const_int 7`
//     defs on different incoming edges). Result aliases to the
//     first const. Saves an iteration vs. waiting for CSE to
//     dedup the constants first.
//
// Phis whose surviving args span ≥2 distinct non-self Values
// (and aren't all the same constant) are not trivial and stay.
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
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
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
			if surviving, ok := trivialPhiTarget(op); ok {
				sub[op.Result.ID] = surviving
				continue
			}
			if surviving, ok := constArgsTarget(op, defs); ok {
				sub[op.Result.ID] = surviving
			}
		}
	}
	if len(sub) == 0 {
		return
	}
	applySubstitutions(f, sub)
}

// constArgsTarget reports whether every non-self-ref arg of
// `op` (assumed OpPhi) resolves to a const Op carrying the
// same immediate value. Returns the first such arg if so —
// aliasing the phi to either const works since they hold the
// same value. Returns false if any arg isn't a const, or the
// const kinds/values don't all match.
//
// Distinct from trivialPhiTarget: this handles the case where
// the args are different SSA Values but represent the same
// compile-time constant. CSE eventually dedups the constants
// and lets trivialPhiTarget pick it up on the next iteration,
// but doing it here saves a fixed-point round-trip.
func constArgsTarget(op *Op, defs map[int32]*Op) (Value, bool) {
	var first Value
	var firstDef *Op
	for _, a := range op.Args {
		if !a.IsValid() {
			return Value{}, false
		}
		if a == op.Result {
			continue
		}
		def, ok := defs[a.ID]
		if !ok || !IsConst(def.Kind) {
			return Value{}, false
		}
		if !first.IsValid() {
			first = a
			firstDef = def
			continue
		}
		if def.Kind != firstDef.Kind {
			return Value{}, false
		}
		switch def.Kind {
		case OpConstInt, OpConstBool:
			if def.Imm != firstDef.Imm {
				return Value{}, false
			}
		case OpConstFloat:
			if def.F64 != firstDef.F64 {
				return Value{}, false
			}
		case OpConstString:
			if def.Str != firstDef.Str {
				return Value{}, false
			}
		default:
			return Value{}, false
		}
	}
	if !first.IsValid() {
		return Value{}, false
	}
	return first, true
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
