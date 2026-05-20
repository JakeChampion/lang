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
	entryInBlocks := false
	for _, b := range f.Blocks {
		if b == f.Entry {
			entryInBlocks = true
			break
		}
	}
	if !entryInBlocks {
		return fmt.Errorf("func %q: Entry block %d is not in Blocks; would skip during walks",
			f.Name, f.Entry.ID)
	}

	// Pass 1: every Block has a terminator + Preds is
	// consistent with successors. Terminator pointer-fields
	// must be non-nil (a dangling Br.Target / BrIf.True /
	// BrIf.False would yield a nil pointer deref downstream).
	for _, b := range f.Blocks {
		if b.Term.Kind == TermInvalid {
			return fmt.Errorf("func %q: block %d has no terminator", f.Name, b.ID)
		}
		switch b.Term.Kind {
		case TermBr:
			if b.Term.Target == nil {
				return fmt.Errorf("func %q: block %d br has nil target", f.Name, b.ID)
			}
		case TermBrIf:
			if b.Term.True == nil {
				return fmt.Errorf("func %q: block %d brif has nil True target", f.Name, b.ID)
			}
			if b.Term.False == nil {
				return fmt.Errorf("func %q: block %d brif has nil False target", f.Name, b.ID)
			}
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
			if op.Result.IsValid() {
				if site, dup := defs[op.Result.ID]; dup {
					return fmt.Errorf("func %q: value %s defined twice (%s and block %d %s)",
						f.Name, op.Result, site, b.ID, op.Kind)
				}
				defs[op.Result.ID] = fmt.Sprintf("block %d %s", b.ID, op.Kind)
			}
			if op.Result2.IsValid() {
				if site, dup := defs[op.Result2.ID]; dup {
					return fmt.Errorf("func %q: value %s defined twice (%s and block %d %s second result)",
						f.Name, op.Result2, site, b.ID, op.Kind)
				}
				defs[op.Result2.ID] = fmt.Sprintf("block %d %s (second)", b.ID, op.Kind)
			}
		}
	}

	// Pass 4: every used Value has a definition somewhere AND
	// (for Op uses + terminator uses) the def-site dominates
	// the use-site. This catches both "no def at all" and the
	// stronger SSA invariant "uses are dominated by defs".
	//
	// "Dominance" here means:
	//   - Params dominate every block (they're in scope on entry).
	//   - An Op's Result dominates: every Op later in the same
	//     block, that block's terminator, and every block that
	//     the defining block dominates in the CFG.
	//
	// Implementation: pre-compute, for each Op result, the (block,
	// index-within-block) where it was defined. Walk uses; if the
	// use's block strictly differs from the def's block, fall back
	// to the dominator tree. Else compare in-block indices.

	type defSite struct {
		block *Block // nil for Params (dominate everything)
		index int    // -1 if a Param; otherwise position in block.Ops
	}
	defSites := map[int32]defSite{}
	for _, p := range f.Params {
		if p.ID == 0 {
			continue
		}
		defSites[p.ID] = defSite{block: nil, index: -1}
	}
	for _, b := range f.Blocks {
		for i, op := range b.Ops {
			if op.Result.IsValid() {
				defSites[op.Result.ID] = defSite{block: b, index: i}
			}
			if op.Result2.IsValid() {
				defSites[op.Result2.ID] = defSite{block: b, index: i}
			}
		}
	}

	dom := BuildDomTree(f)

	dominatesUse := func(def defSite, useBlock *Block, useIndex int) bool {
		if def.block == nil {
			return true // Param
		}
		if def.block == useBlock {
			// Same block: def must come strictly before use.
			// useIndex == len(useBlock.Ops) is the terminator slot.
			return def.index < useIndex
		}
		return dom.Dominates(def.block, useBlock)
	}

	check := func(arg Value, useBlock *Block, useIndex int, what string) error {
		if !arg.IsValid() {
			return nil
		}
		if _, ok := defs[arg.ID]; !ok {
			return fmt.Errorf("func %q: block %d %s uses undefined value %s",
				f.Name, useBlock.ID, what, arg)
		}
		site, ok := defSites[arg.ID]
		if !ok {
			// Shouldn't happen if defs and defSites agree, but
			// fall back to the def-existence check above so we
			// never silently accept.
			return fmt.Errorf("func %q: block %d %s uses value %s with no def site",
				f.Name, useBlock.ID, what, arg)
		}
		if !dominatesUse(site, useBlock, useIndex) {
			return fmt.Errorf("func %q: block %d %s uses %s before its def dominates the use",
				f.Name, useBlock.ID, what, arg)
		}
		return nil
	}

	for _, b := range f.Blocks {
		sawNonPhi := false
		for i, op := range b.Ops {
			if op.Kind == OpPhi {
				if sawNonPhi {
					return fmt.Errorf("func %q: block %d has phi at index %d after a non-phi Op; phis must lead the block",
						f.Name, b.ID, i)
				}
				if len(op.Args) != len(b.Preds) {
					return fmt.Errorf("func %q: block %d phi %s has %d args but block has %d preds",
						f.Name, b.ID, op.Result, len(op.Args), len(b.Preds))
				}
				// Phi-operand dominance: Args[j] is the value
				// flowing in from Preds[j]. Its def must dominate
				// the END of Preds[j], not this block. We model
				// "end of P" as a use at index len(P.Ops) (the
				// terminator slot) within P.
				for j, arg := range op.Args {
					pred := b.Preds[j]
					if err := check(arg, pred, len(pred.Ops), "phi"); err != nil {
						return err
					}
				}
				continue
			}
			sawNonPhi = true
			for _, arg := range op.Args {
				if err := check(arg, b, i, op.Kind.String()); err != nil {
					return err
				}
			}
		}
		// Terminator uses live in the slot just past the last Op.
		termIdx := len(b.Ops)
		switch b.Term.Kind {
		case TermBrIf:
			if err := check(b.Term.Cond, b, termIdx, "brif"); err != nil {
				return err
			}
		case TermRet:
			if err := check(b.Term.Value, b, termIdx, "ret"); err != nil {
				return err
			}
		case TermRetPair:
			if err := check(b.Term.Value, b, termIdx, "ret_pair tag"); err != nil {
				return err
			}
			if err := check(b.Term.Value2, b, termIdx, "ret_pair payload"); err != nil {
				return err
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
