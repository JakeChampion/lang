package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// Sum field identities specify their active variant. Prove it from the actual
// immutable container SSA value and exclusive control-flow edges, independently
// of source pattern metadata or the builder's exhaustiveness calculation.
func verifyEnumGuards(f *Func, dom *ssa.DomTree) error {
	type guard struct {
		block   *ssa.Block
		variant int
		present bool
	}
	defs := make(map[int32]*ssa.Op)
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpSumMake || op.Kind == ssa.OpSumIs {
				defs[op.Result.ID] = op
			}
		}
	}
	guards := make(map[int32][]guard)
	for _, block := range f.graph.Blocks {
		term := block.Term
		if term.Kind != ssa.TermBrIf || term.True == term.False {
			continue
		}
		test := defs[term.Cond.ID]
		if test == nil || test.Kind != ssa.OpSumIs || len(test.Args) != 1 {
			continue
		}
		for _, branch := range []struct {
			target  *ssa.Block
			present bool
		}{{term.True, true}, {term.False, false}} {
			if branch.target != nil && len(branch.target.Preds) == 1 && branch.target.Preds[0] == block {
				id := test.Args[0].ID
				guards[id] = append(guards[id], guard{branch.target, int(test.Imm), branch.present})
			}
		}
	}
	type query struct {
		block     *ssa.Block
		container int32
		variant   int
	}
	proven := make(map[query]bool)
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind != ssa.OpSumGet {
				continue
			}
			if len(op.Args) != 1 {
				return fmt.Errorf("semir: malformed sum projection")
			}
			container := op.Args[0]
			e := f.program.enum(f.values[container.ID].typ.source)
			if e == nil || op.Imm < 0 || op.Imm >= int64(len(e.fields)) {
				return fmt.Errorf("semir: unresolved sum projection")
			}
			variant := e.fields[op.Imm].variant
			q := query{block, container.ID, variant}
			if proven[q] {
				continue
			}
			if make := defs[container.ID]; make != nil && make.Kind == ssa.OpSumMake && make.Imm == int64(variant) {
				proven[q] = true
				continue
			}
			excluded := make([]bool, len(e.variants))
			for _, g := range guards[container.ID] {
				if g.variant < 0 || g.variant >= len(e.variants) || !dom.Dominates(g.block, block) {
					continue
				}
				if g.present && g.variant == variant {
					proven[q] = true
					break
				}
				if !g.present {
					excluded[g.variant] = true
				}
			}
			if !proven[q] {
				remaining := 0
				for i, no := range excluded {
					if !no && i != variant {
						remaining++
					}
				}
				proven[q] = remaining == 0
			}
			if !proven[q] {
				pos := f.values[op.Result.ID].pos
				return fmt.Errorf("semir %s at %d:%d: enum payload lacks an active-variant proof for %s", f.graph.Name, pos.Line, pos.Col, container)
			}
		}
	}
	return nil
}
