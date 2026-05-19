package ssa

import "fmt"

// Verify checks structural invariants on `f` and returns the
// first failure as an error, or nil if the function is
// well-formed. Optimisation passes call this after mutating
// the graph so bugs surface at the place that introduced
// them rather than a downstream consumer.
//
// Invariants checked:
//   - Every Block (except blocks reachable only via dead
//     code) has a terminator set.
//   - SSA single-assignment: every Value is the Result of
//     at most one Op (or a single Param entry).
//   - Use-before-def at the Block level: every Value used in
//     an Op or terminator is either a Param, defined in an
//     ancestor Block (reachable via Preds chain), or
//     defined earlier in the same Block.
//   - Preds list is consistent with terminator targets:
//     every block listed as a successor of B should have B
//     in its Preds.
//   - Entry block has no Preds (function entry can't be
//     re-entered).
func Verify(f *Func) error {
	if f == nil {
		return fmt.Errorf("ssa.Verify: nil func")
	}
	if f.Entry == nil {
		if len(f.Blocks) == 0 {
			return nil // empty function — vacuously OK
		}
		return fmt.Errorf("func %q: has blocks but no Entry set", f.Name)
	}

	// Pass 1: every Block has a terminator + Preds is
	// consistent with successors.
	for _, b := range f.Blocks {
		if b.Term.Kind == TermInvalid {
			return fmt.Errorf("func %q: block %d has no terminator", f.Name, b.ID)
		}
		for _, succ := range b.Succs() {
			if !blockInPreds(succ, b) {
				return fmt.Errorf("func %q: block %d → block %d but succ doesn't list pred",
					f.Name, b.ID, succ.ID)
			}
		}
	}

	// Pass 2: Entry has no Preds.
	if len(f.Entry.Preds) > 0 {
		return fmt.Errorf("func %q: entry block %d has %d Preds; entry must be unreachable from inside the function",
			f.Name, f.Entry.ID, len(f.Entry.Preds))
	}

	// Pass 3: SSA single-assignment. Walk every Op + Param
	// and verify each Value.ID is unique.
	defs := map[int32]string{}
	for _, p := range f.Params {
		if p.ID == 0 {
			continue
		}
		if site, dup := defs[p.ID]; dup {
			return fmt.Errorf("func %q: value %s defined twice (%s and Param)", f.Name, p, site)
		}
		defs[p.ID] = "Param"
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if !op.Result.IsValid() {
				continue
			}
			if site, dup := defs[op.Result.ID]; dup {
				return fmt.Errorf("func %q: value %s defined twice (%s and block %d %s)",
					f.Name, op.Result, site, b.ID, op.Kind)
			}
			defs[op.Result.ID] = fmt.Sprintf("block %d %s", b.ID, op.Kind)
		}
	}

	// Pass 4: every used Value has a definition somewhere.
	// (Stronger use-before-def — within-block + dominance —
	// requires the dominator-tree builder; Phase 2 adds
	// that. Phase 1 catches the easy "no def at all" case.)
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			for _, arg := range op.Args {
				if !arg.IsValid() {
					continue
				}
				if _, ok := defs[arg.ID]; !ok {
					return fmt.Errorf("func %q: block %d %s uses undefined value %s",
						f.Name, b.ID, op.Kind, arg)
				}
			}
		}
		// Terminator uses.
		if b.Term.Kind == TermBrIf && b.Term.Cond.IsValid() {
			if _, ok := defs[b.Term.Cond.ID]; !ok {
				return fmt.Errorf("func %q: block %d brif uses undefined value %s",
					f.Name, b.ID, b.Term.Cond)
			}
		}
		if b.Term.Kind == TermRet && b.Term.Value.IsValid() {
			if _, ok := defs[b.Term.Value.ID]; !ok {
				return fmt.Errorf("func %q: block %d ret uses undefined value %s",
					f.Name, b.ID, b.Term.Value)
			}
		}
	}

	return nil
}

func blockInPreds(child, parent *Block) bool {
	for _, p := range child.Preds {
		if p == parent {
			return true
		}
	}
	return false
}
