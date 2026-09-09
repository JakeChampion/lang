package semir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// Forward execution tracks an expired witness until its definition executes
// again. This does not use the production backward walk or lifetime mask helper.
// This oracle isolates the intervening-end relation, not snapshot payload or
// initialization semantics. Fresh observations can be absent: the separate epoch
// promotion/native tests check this across real loop-latch lifetime resets.
func guardedWitnessOracle(start, write, end *ssa.Block) bool {
	type state struct {
		block   *ssa.Block
		expired bool
	}
	queue := []state{{block: start}}
	seen := make(map[state]bool)
	for next := 0; next < len(queue); next++ {
		current := queue[next]
		if current.block == start {
			current.expired = false
		}
		if current.block == write && current.expired {
			return false
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		current.expired = current.expired || current.block == end
		for _, succ := range current.block.Succs() {
			queue = append(queue, state{succ, current.expired})
		}
	}
	return true
}

func TestGuardedBindingWitnessMatchesLifetimeOracle(t *testing.T) {
	// Scope/guard/type contracts have separate full-Verify tests. These synthetic
	// graphs isolate the lifetime query on valid SSA, including irreducible flow.
	shapes := [][][]int{
		{{1}, {2}, {3}, {}},
		{{1, 2}, {3}, {3}, {4}, {}},
		{{1}, {2, 3}, {1}, {}},
		{{1}, {2, 5}, {3, 4}, {2}, {1}, {}},
		{{1, 2}, {3}, {3}, {1, 4}, {}},
	}
	cases, accepted, rejected := 0, 0, 0
	for _, shape := range shapes {
		for snapshotAt := range shape {
			for writeAt := range shape {
				for otherWrite := -1; otherWrite < len(shape); otherWrite++ {
					for endAt := -1; endAt < len(shape); endAt++ {
						f := newFunc("lifetime-oracle", ast.VoidType{})
						flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
						for range shape {
							f.graph.NewBlock()
						}
						blocks := f.graph.Blocks
						for i, successors := range shape {
							switch len(successors) {
							case 0:
								f.graph.SetRet(blocks[i], ssa.Value{})
							case 1:
								f.graph.SetBr(blocks[i], blocks[successors[0]])
							case 2:
								f.graph.SetBrIf(blocks[i], flag, blocks[successors[0]], blocks[successors[1]])
							}
						}
						dom := ssa.BuildDomTree(f.graph)
						if !dom.Dominates(blocks[snapshotAt], blocks[writeAt]) ||
							(otherWrite >= 0 && !dom.Dominates(blocks[snapshotAt], blocks[otherWrite])) {
							continue
						}
						id := f.addBinding("witnessed", ast.BoolType{}, ast.Position{})
						scope := &cleanupBoundary{owner: f}
						f.bindings[id-1].boundary = scope
						witness := f.snapshotBinding(blocks[snapshotAt], id, ast.Position{})
						f.replaceBindingGuarded(blocks[writeAt], id, witness, flag, ast.Position{})
						if otherWrite >= 0 {
							f.replaceBindingGuarded(blocks[otherWrite], id, witness, flag, ast.Position{})
						}
						var end *ssa.Block
						if endAt >= 0 {
							end = blocks[endAt]
							f.cleanupExits = []*cleanupExit{{ends: []cleanupEnd{{scope, end}}}}
						}
						if err := ssa.Verify(f.graph); err != nil {
							t.Fatal(err)
						}
						want := guardedWitnessOracle(blocks[snapshotAt], blocks[writeAt], end)
						if otherWrite >= 0 {
							want = want && guardedWitnessOracle(blocks[snapshotAt], blocks[otherWrite], end)
						}
						err := verifyGuardedBindingWrites(f)
						if (err == nil) != want {
							t.Fatalf("shape=%v snapshot=%d write=%d other=%d end=%d: got %v, oracle=%t", shape, snapshotAt, writeAt, otherWrite, endAt, err, want)
						}
						cases++
						if want {
							accepted++
						} else {
							rejected++
						}
					}
				}
			}
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatal("oracle must exercise accepted and rejected lifetimes")
	}
	t.Logf("%d lifetime comparisons: %d accepted, %d rejected", cases, accepted, rejected)
}
