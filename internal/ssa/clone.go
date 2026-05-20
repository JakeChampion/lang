package ssa

// Clone returns a deep copy of `f`. Every Block and Op gets a
// fresh allocation; pointer fields (Preds, Op.Args sharing,
// terminator targets) are rewritten through a Block-pointer
// map so the clone is self-contained — mutating it doesn't
// affect the original and vice-versa.
//
// Value IDs are preserved exactly. This keeps `Value{ID: 5}`
// pointing at the same logical value in both copies, which
// matters when consumers cache external indices keyed by
// Value.ID across the clone.
//
// Block IDs are also preserved. Same reasoning.
//
// Use cases:
//   - Optimisation passes that want to try a transformation
//     and revert if it doesn't improve the function.
//   - Differential testing — clone, run pass A on one, pass
//     B on the other, compare outputs.
//   - Concurrency: each goroutine gets a private copy so
//     analyses don't race.
func (f *Func) Clone() *Func {
	if f == nil {
		return nil
	}
	out := &Func{
		Name:        f.Name,
		Params:      append([]Value(nil), f.Params...),
		nextValueID: f.nextValueID,
		nextBlockID: f.nextBlockID,
	}

	// Map old → new Block pointers so we can rewrite Preds and
	// terminator targets.
	blockMap := make(map[*Block]*Block, len(f.Blocks))
	out.Blocks = make([]*Block, len(f.Blocks))
	for i, src := range f.Blocks {
		dst := &Block{ID: src.ID}
		blockMap[src] = dst
		out.Blocks[i] = dst
	}
	if f.Entry != nil {
		out.Entry = blockMap[f.Entry]
	}

	// Walk again to copy contents now that all destinations
	// exist (so terminator/Preds pointer rewrites resolve).
	for _, src := range f.Blocks {
		dst := blockMap[src]

		// Ops — each gets a fresh allocation; Args slices
		// are duplicated so independent mutation is safe.
		if len(src.Ops) > 0 {
			dst.Ops = make([]*Op, len(src.Ops))
			for i, op := range src.Ops {
				dst.Ops[i] = &Op{
					Kind:    op.Kind,
					Result:  op.Result,
					Result2: op.Result2,
					Args:    append([]Value(nil), op.Args...),
					Imm:     op.Imm,
					F64:     op.F64,
					Str:     op.Str,
				}
			}
		}

		// Preds — rewrite through the block map.
		if len(src.Preds) > 0 {
			dst.Preds = make([]*Block, len(src.Preds))
			for i, p := range src.Preds {
				dst.Preds[i] = blockMap[p]
			}
		}

		// Terminator — rewrite pointer fields.
		dst.Term = Terminator{
			Kind:  src.Term.Kind,
			Cond:   src.Term.Cond,
			Value:  src.Term.Value,
			Value2: src.Term.Value2,
		}
		if src.Term.Target != nil {
			dst.Term.Target = blockMap[src.Term.Target]
		}
		if src.Term.True != nil {
			dst.Term.True = blockMap[src.Term.True]
		}
		if src.Term.False != nil {
			dst.Term.False = blockMap[src.Term.False]
		}
	}

	return out
}
