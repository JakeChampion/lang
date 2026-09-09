package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// Explore exact (block, initialized) states directly, without using production
// reaching-definition caches, dominance, masks or a loop-iteration bound.
func snapshotPresenceOracle(f *Func, snapshot *ssa.Op) uint8 {
	type state struct {
		block       *ssa.Block
		initialized bool
	}
	queue := []state{{block: f.graph.Entry}}
	seen := map[state]bool{queue[0]: true}
	var result uint8
	for next := 0; next < len(queue); next++ {
		at := queue[next]
		for _, op := range at.block.Ops {
			if op == snapshot {
				if at.initialized {
					result |= 2
				} else {
					result |= 1
				}
			}
			if op.Kind == ssa.OpBindingInit {
				at.initialized = true
			}
		}
		for _, successor := range at.block.Succs() {
			incoming := state{successor, at.initialized}
			if !seen[incoming] {
				seen[incoming] = true
				queue = append(queue, incoming)
			}
		}
	}
	return result
}

func promotedPresence(f *Func, value ssa.Value) uint8 {
	facts := make(map[int32]uint8)
	for changed := true; changed; {
		changed = false
		for _, block := range f.graph.Blocks {
			for _, op := range block.Ops {
				var next uint8
				switch op.Kind {
				case ssa.OpStateAbsent:
					next = 1
				case ssa.OpStatePresent:
					next = 2
				case ssa.OpPhi:
					for _, arg := range op.Args {
						next |= facts[arg.ID]
					}
				}
				if next != facts[op.Result.ID] {
					facts[op.Result.ID] = next
					changed = true
				}
			}
		}
	}
	return facts[value.ID]
}

func TestBindingSnapshotPromotionMatchesExactCFGOracle(t *testing.T) {
	for _, shape := range []struct {
		name  string
		edges [][2]int
	}{
		{"diamond", [][2]int{{1, 2}, {3, 3}, {3, 3}, {-1, -1}}},
		{"loop", [][2]int{{1, 1}, {2, 3}, {1, 1}, {-1, -1}}},
		{"nested-loops", [][2]int{{1, 1}, {2, 5}, {3, 4}, {2, 2}, {1, 1}, {-1, -1}}},
		{"irreducible-cycle", [][2]int{{1, 2}, {2, 3}, {1, 3}, {-1, -1}}},
	} {
		for initBlock := range shape.edges {
			for readBlock := range shape.edges {
				for _, first := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/init-%d/snapshot-%d/first-%t", shape.name, initBlock, readBlock, first), func(t *testing.T) {
						f, entry, id, payload := bindingTestFunc()
						flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
						blocks := []*ssa.Block{entry}
						for len(blocks) < len(shape.edges) {
							blocks = append(blocks, f.graph.NewBlock())
						}
						var snapshot ssa.Value
						if first {
							snapshot = f.snapshotBinding(blocks[readBlock], id, ast.Position{})
						}
						f.writeBinding(blocks[initBlock], ssa.OpBindingInit, id, payload, ast.Position{})
						if !first {
							snapshot = f.snapshotBinding(blocks[readBlock], id, ast.Position{})
						}
						var snapshotOp *ssa.Op
						for _, op := range blocks[readBlock].Ops {
							if op.Result == snapshot {
								snapshotOp = op
							}
						}
						f.addOp(blocks[readBlock], ssa.OpStateHas, ast.BoolType{}, ast.Position{}, snapshot)
						observer := blocks[readBlock].Ops[len(blocks[readBlock].Ops)-1]
						for i, edge := range shape.edges {
							switch {
							case edge[0] < 0:
								f.graph.SetRet(blocks[i], payload)
							case edge[0] == edge[1]:
								f.graph.SetBr(blocks[i], blocks[edge[0]])
							default:
								f.graph.SetBrIf(blocks[i], flag, blocks[edge[0]], blocks[edge[1]])
							}
						}
						want := snapshotPresenceOracle(f, snapshotOp)
						if err := promoteBindings(f); err != nil {
							t.Fatal(err)
						}
						if err := Verify(f); err != nil {
							t.Fatal(err)
						}
						if got := promotedPresence(f, observer.Args[0]); got != want {
							t.Fatalf("promoted alternatives %b differ from exact oracle %b", got, want)
						}
						for _, block := range f.graph.Blocks {
							for _, op := range block.Ops {
								if op.Kind == ssa.OpStatePresent && op.Args[0] != payload {
									t.Fatal("promotion changed initializer payload")
								}
							}
						}
					})
				}
			}
		}
	}
}
