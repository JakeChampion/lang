package ssa

// LICM — loop-invariant code motion. Walks every natural loop
// in `f` and hoists pure ops whose operands are all defined
// outside the loop into the loop's preheader. Returns the
// number of ops hoisted.
//
// What counts as invariant:
//   - Op is pure (IsPure(op.Kind) — Call/Load/Store/Alloc all
//     skipped).
//   - Op is NOT a phi (phi values vary by iteration by
//     definition).
//   - Op is NOT a divide / remainder (could trap on a path
//     where the loop body never executes; hoisting that out
//     of the loop would cause a trap that didn't happen
//     before). Conservatively skip these even when we could
//     prove the divisor non-zero.
//   - Every Op.Arg is either a Param or defined in a block
//     OUTSIDE the loop.
//
// Preheader requirement: each loop must have a unique block
// outside the loop body whose terminator is an unconditional
// `br loop.Header`. Loops with multiple entries or with a
// brif into the header are skipped (LICM is a non-issue for
// the IR-lifted shape; this just stays safe).
//
// Iteration: hoist sweeps the loop body repeatedly until no
// more ops can be lifted. Each lifted op updates the
// blockOf map so subsequent sweeps see its new def-site
// outside the loop — that's how a chain of ops where each
// depends on the previous all hoist out together.
//
// Pair with DCE if any hoisted op leaves dead siblings in
// the loop body (Simplify-style aliasing already handles
// the common cases).
func LICM(f *Func) int {
	if f == nil || f.Entry == nil {
		return 0
	}
	loops := Loops(f)
	if len(loops) == 0 {
		return 0
	}

	blockOf := buildBlockOf(f)
	hoisted := 0
	for _, lp := range loops {
		preheader := findPreheader(lp)
		if preheader == nil {
			continue
		}
		hoisted += hoistLoop(lp, preheader, blockOf)
	}
	return hoisted
}

// buildBlockOf indexes every Op.Result Value to its defining
// Block. Params have no defining block — they map to nil
// (i.e. they're absent from the map; callers check that).
func buildBlockOf(f *Func) map[int32]*Block {
	out := map[int32]*Block{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				out[op.Result.ID] = b
			}
			if op.Result2.IsValid() {
				out[op.Result2.ID] = b
			}
		}
	}
	return out
}

// findPreheader returns the loop's preheader — the unique
// block outside lp.Body whose terminator is `br lp.Header`.
// Returns nil if the loop has multiple entries, a brif into
// the header, or no proper preheader at all.
func findPreheader(lp *Loop) *Block {
	var pre *Block
	for _, p := range lp.Header.Preds {
		if lp.Body[p] {
			continue // back-edge tail
		}
		if pre != nil {
			return nil // multiple external preds
		}
		pre = p
	}
	if pre == nil {
		return nil
	}
	if pre.Term.Kind != TermBr || pre.Term.Target != lp.Header {
		return nil
	}
	return pre
}

// hoistLoop sweeps the loop body until no more invariant
// ops can be lifted. Returns the count of ops moved.
func hoistLoop(lp *Loop, preheader *Block, blockOf map[int32]*Block) int {
	hoisted := 0
	for {
		didHoist := false
		for b := range lp.Body {
			i := 0
			for i < len(b.Ops) {
				op := b.Ops[i]
				if isLoopInvariant(op, lp, blockOf) {
					// Remove from b at i.
					b.Ops = append(b.Ops[:i], b.Ops[i+1:]...)
					// Append to preheader's op list.
					preheader.Ops = append(preheader.Ops, op)
					// Update the def-site map.
					if op.Result.IsValid() {
						blockOf[op.Result.ID] = preheader
					}
					if op.Result2.IsValid() {
						blockOf[op.Result2.ID] = preheader
					}
					didHoist = true
					hoisted++
					continue // re-examine new b.Ops[i]
				}
				i++
			}
		}
		if !didHoist {
			break
		}
	}
	return hoisted
}

// isLoopInvariant reports whether `op` (in some block of lp)
// can safely be hoisted to the preheader.
func isLoopInvariant(op *Op, lp *Loop, blockOf map[int32]*Block) bool {
	if op == nil || op.Kind == OpPhi {
		return false
	}
	if !IsPure(op.Kind) {
		return false
	}
	// Divide/remainder can trap if the divisor is zero. Hoisting
	// would cause the trap on a path where the loop body might
	// never execute. Conservative skip.
	switch op.Kind {
	case OpDiv, OpDivU, OpRem, OpRemU:
		return false
	}
	if !op.Result.IsValid() {
		return false
	}
	for _, a := range op.Args {
		if !a.IsValid() {
			continue
		}
		defBlock, ok := blockOf[a.ID]
		if !ok {
			// Param — defined "outside" every loop.
			continue
		}
		if lp.Body[defBlock] {
			return false // operand defined inside the loop
		}
	}
	return true
}
