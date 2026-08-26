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
//
//   - `phi v, v, …, v` (all incoming values identical) → `v`.
//
//   - `phi v, phi-result, v, phi-result, v` (self-references
//     allowed; the iterative phi-cycle in a loop header
//     usually has the self-ref as one arg). Treated as
//     trivial when, ignoring self-refs, only one distinct
//     Value remains.
//
//   - `phi c1, c2` where c1 and c2 are distinct const Ops with
//     identical immediate (e.g. two separate `const_int 7`
//     defs on different incoming edges). The phi is REPLACED by
//     a const Op of its own, in its own block, keeping the phi's
//     Result — not aliased to c1.
//
//     Aliasing is what the other cases do, and here it is
//     unsound: a phi block has two or more preds, so a const
//     defined in ONE of them does not dominate the merge.
//     `phi v4, v5` became `v5` and left `ret v5` in a block v5
//     could not reach, which the arm64 SSA backend rejected on
//     `X && (true && !(!false))` — "ret uses v5 before its def
//     dominates the use" — rather than miscompiling it. Nothing
//     downstream recovered it either: CSE will not dedup the two
//     consts across a diamond for the same dominance reason, so
//     a guard that merely declined would have dropped the
//     optimisation outright. Materialising keeps it, and the
//     new const lands after the block's remaining phis because
//     Verify requires phis to lead a block.
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
	// Phis to rewrite in place into a const of their own, keyed by the
	// block holding them.
	materialise := map[*Block][]*Op{}
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
			if model, ok := constArgsModel(op, defs); ok {
				op.Kind = model.Kind
				op.Args = nil
				op.Imm = model.Imm
				op.F64 = model.F64
				op.Str = model.Str
				materialise[b] = append(materialise[b], op)
			}
		}
	}
	for b, consts := range materialise {
		reorderPhisFirst(b, consts)
	}
	if len(sub) == 0 {
		return
	}
	applySubstitutions(f, sub)
}

// reorderPhisFirst moves `consts` — Ops in b that were phis a moment ago
// and are now const Ops carrying the same Results — to sit directly after
// b's remaining phis, which Verify requires to lead the block. Their args
// are gone, so no use can precede them: every non-phi op in b already
// followed the phis they replaced.
func reorderPhisFirst(b *Block, consts []*Op) {
	moved := map[*Op]bool{}
	for _, op := range consts {
		moved[op] = true
	}
	phis := make([]*Op, 0, len(b.Ops))
	rest := make([]*Op, 0, len(b.Ops))
	for _, op := range b.Ops {
		if moved[op] {
			continue
		}
		if op.Kind == OpPhi {
			phis = append(phis, op)
			continue
		}
		rest = append(rest, op)
	}
	b.Ops = append(append(phis, consts...), rest...)
}

// constArgsModel reports whether every non-self-ref arg of
// `op` (assumed OpPhi) resolves to a const Op carrying the
// same immediate value, and returns one of those const Ops to
// copy the constant from. The CALLER rewrites the phi into a
// const of its own rather than aliasing it to that Op — see
// the const-args case on TrivialPhis for why aliasing here is
// unsound.
//
// Distinct from trivialPhiTarget: this handles the case where
// the args are different SSA Values but represent the same
// compile-time constant.
func constArgsModel(op *Op, defs map[int32]*Op) (*Op, bool) {
	var first Value
	var firstDef *Op
	for _, a := range op.Args {
		if !a.IsValid() {
			return nil, false
		}
		if a == op.Result {
			continue
		}
		def, ok := defs[a.ID]
		if !ok || !IsConst(def.Kind) {
			return nil, false
		}
		if !first.IsValid() {
			first = a
			firstDef = def
			continue
		}
		if def.Kind != firstDef.Kind {
			return nil, false
		}
		switch def.Kind {
		case OpConstInt, OpConstBool:
			if def.Imm != firstDef.Imm {
				return nil, false
			}
		case OpConstFloat:
			if def.F64 != firstDef.F64 {
				return nil, false
			}
		case OpConstString:
			if def.Str != firstDef.Str {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	if !first.IsValid() {
		return nil, false
	}
	return firstDef, true
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
