package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// Presence of an old snapshot is not proof that its place still exists. Run
// after structural/boundary verification and the exact-state guard proof, but
// before promotion erases snapshot and place identities.
func verifyGuardedBindingWrites(f *Func) error {
	type definition struct {
		block *ssa.Block
		op    *ssa.Op
		next  int
	}
	type snapshot struct {
		block       *ssa.Block
		op          *ssa.Op
		first, last int
	}
	snapshots := make(map[int32]snapshot)
	var writes []definition
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			switch op.Kind {
			case ssa.OpBindingSnapshot:
				snapshots[op.Result.ID] = snapshot{block, op, -1, -1}
			case ssa.OpBindingReplaceGuarded:
				writes = append(writes, definition{block, op, -1})
			}
		}
	}
	fail := func(op *ssa.Op, message string) error {
		pos := f.effectPositions[op]
		return fmt.Errorf("semir %s binding %d at %d:%d: %s", f.graph.Name, op.Imm, pos.Line, pos.Col, message)
	}
	// Flat linked indices group uses without one allocation per witness/use.
	// The groups and their writes retain first-seen order for stable diagnostics.
	var groups []int32
	for i, write := range writes {
		op := write.op
		witness := snapshots[op.Args[0].ID]
		if witness.op == nil || witness.op.Imm != op.Imm {
			return fail(op, "guarded replacement requires a snapshot of the same binding")
		}
		if witness.first == -1 {
			witness.first = i
			groups = append(groups, op.Args[0].ID)
		} else {
			writes[witness.last].next = i
		}
		witness.last = i
		snapshots[op.Args[0].ID] = witness
	}
	ends := bindingLifetimeEnds(f)
	seen := make(map[*ssa.Block]bool)
	var queue []*ssa.Block
	for _, group := range groups {
		witness := snapshots[group]
		clear(seen)
		for index := witness.first; index != -1; index = writes[index].next {
			write := writes[index]
			op := write.op
			if seen[write.block] {
				continue
			}
			// SSA dominance established that the exact snapshot precedes this
			// write. Re-executing its definition creates a fresh observation.
			queue = append(queue[:0], write.block)
			seen[write.block] = true
			for next := 0; next < len(queue); next++ {
				block := queue[next]
				if block == witness.block {
					continue
				}
				for _, pred := range block.Preds {
					// Check the edge's lifetime end even if its predecessor entry
					// was already proven safe for an earlier write in this group.
					if mask := ends[pred]; mask != nil && mask[(op.Imm-1)/64]&(uint64(1)<<((op.Imm-1)%64)) != 0 {
						return fail(op, "guarded replacement witness crosses a binding lifetime end")
					}
					if !seen[pred] {
						seen[pred] = true
						queue = append(queue, pred)
					}
				}
			}
			// A successful query closed every predecessor edge in this set,
			// stopping only at the snapshot. It is reusable for this witness
			// throughout this immutable verification call, not across edits.
		}
	}
	return nil
}
