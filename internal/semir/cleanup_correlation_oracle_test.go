package semir

import (
	"slices"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// This oracle executes complete action stacks and registration history. It
// shares neither projected states nor transfer helpers with production. Histories
// cannot register an identity twice, so its state space is finite even on cycles;
// no execution-depth bound or production fixed-point routine is needed.
func cleanupStackOracle(f *Func, flow *cleanupBoundaryFlow) bool {
	type state struct {
		block       *ssa.Block
		stack       string
		history     uint8
		initialized bool
	}
	ids := make(map[*cleanupRegion]byte)
	for i, r := range f.cleanups {
		ids[r] = byte(i + 1)
	}
	first := state{block: f.graph.Entry}
	seen, queue := map[state]bool{first: true}, []state{first}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		point := flow.points[current.block]
		for _, op := range current.block.Ops {
			if op.Kind == ssa.OpBindingInit {
				current.initialized = true
			}
		}
		for _, r := range point.registers {
			id := ids[r]
			if current.history&(1<<id) != 0 {
				return false
			}
			current.history |= 1 << id
			current.stack += string([]byte{id})
		}
		if r := point.replay; r != nil {
			id := ids[r]
			if current.history&(1<<id) != 0 {
				if len(current.stack) == 0 || current.stack[len(current.stack)-1] != id {
					return false
				}
				if len(r.captures) != 0 && !current.initialized {
					return false
				}
				current.stack = current.stack[:len(current.stack)-1]
			}
		}
		if len(point.ends) != 0 {
			if current.stack != "" {
				return false
			}
			current.history, current.initialized = 0, false
		}
		if current.block.Term.Kind == ssa.TermRet && (current.stack != "" || current.history != 0) {
			return false
		}
		for _, succ := range current.block.Succs() {
			out := current
			out.block = succ
			if !seen[out] {
				seen[out] = true
				queue = append(queue, out)
			}
		}
	}
	return true
}

func TestCleanupCorrelationMatchesFullStackOracle(t *testing.T) {
	// Exercise every event permutation, including all registration and replay
	// orders. The graph families add bypasses, joins, cycles and lifetime resets.
	// Scope identity itself is tested separately through complete Verify calls.
	cases, accepted, rejected := 0, 0, 0
	for _, captures := range []bool{false, true} {
		events := []int{0, 1, 2, 3, 4, 5}
		var permute func(int)
		permute = func(at int) {
			if at != len(events) {
				for i := at; i < len(events); i++ {
					events[at], events[i] = events[i], events[at]
					permute(at + 1)
					events[at], events[i] = events[i], events[at]
				}
				return
			}
			for shape := 0; shape < 8; shape++ {
				f := newFunc("oracle", ast.VoidType{})
				for range 8 {
					f.graph.NewBlock()
				}
				scope := &cleanupBoundary{owner: f}
				flow := &cleanupBoundaryFlow{points: make(map[*ssa.Block]*cleanupFlowPoint)}
				for _, block := range f.graph.Blocks {
					flow.points[block] = &cleanupFlowPoint{}
				}
				for range 3 {
					f.cleanups = append(f.cleanups, &cleanupRegion{owner: f, boundary: scope})
				}
				if captures {
					f.bindings = append(f.bindings, binding{typ: ast.BoolType{}, boundary: scope})
					f.cleanups[0].captures = []BindingID{1}
				}
				for i, event := range events {
					block := f.graph.Blocks[i+1]
					if event < 3 {
						flow.points[block].registers = []*cleanupRegion{f.cleanups[event]}
						if captures && event == shape%3 {
							block.Ops = append(block.Ops, &ssa.Op{Kind: ssa.OpBindingInit, Imm: 1})
						}
					} else {
						flow.points[block].replay = f.cleanups[event-3]
					}
				}
				blocks := f.graph.Blocks
				for i := range len(blocks) - 1 {
					f.graph.SetBr(blocks[i], blocks[i+1])
				}
				f.graph.SetRet(blocks[7], ssa.Value{})
				flow.points[blocks[7]].ends = []*cleanupBoundary{scope}
				switch shape {
				case 1:
					f.graph.SetBrIf(blocks[0], ssa.Value{}, blocks[1], blocks[3])
				case 2:
					f.graph.SetBrIf(blocks[1], ssa.Value{}, blocks[2], blocks[4])
				case 3:
					f.graph.SetBrIf(blocks[6], ssa.Value{}, blocks[2], blocks[7])
				case 4:
					f.graph.SetBrIf(blocks[0], ssa.Value{}, blocks[1], blocks[3])
					f.graph.SetBrIf(blocks[5], ssa.Value{}, blocks[2], blocks[6])
				case 5:
					flow.points[blocks[6]].ends = []*cleanupBoundary{scope}
					f.graph.SetBrIf(blocks[6], ssa.Value{}, blocks[1], blocks[7])
				case 6:
					f.graph.SetBrIf(blocks[0], ssa.Value{}, blocks[1], blocks[4])
					f.graph.SetBrIf(blocks[3], ssa.Value{}, blocks[4], blocks[7])
				case 7:
					f.graph.SetBrIf(blocks[6], ssa.Value{}, blocks[6], blocks[7])
				}
				want := cleanupStackOracle(f, flow)
				err := verifyCleanupProjections(f, flow)
				if (err == nil) != want {
					t.Fatalf("events=%v shape=%d captures=%v: projected=%v oracle=%v", events, shape, captures, err, want)
				}
				cases++
				if want {
					accepted++
				} else {
					rejected++
				}
			}
		}
		permute(0)
		if !slices.Equal(events, []int{0, 1, 2, 3, 4, 5}) {
			t.Fatal("permutation generator did not restore its input")
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatal("oracle did not exercise both acceptance and rejection")
	}
	t.Logf("%d exact finite-state comparisons: %d accepted, %d rejected", cases, accepted, rejected)
}
