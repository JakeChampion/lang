package semir

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// This oracle executes complete action stacks and registration history. It
// shares neither projected states nor transfer helpers with production. Histories
// cannot register an identity twice, so its state space is finite even on cycles;
// no execution-depth bound or production fixed-point routine is needed.
// Ends drain/reset only their own actions and places. Outer pending actions and
// initialization survive an inner end, while inner history resets on re-entry.
func cleanupStackOracle(f *Func, flow *cleanupBoundaryFlow) bool {
	type state struct {
		block       *ssa.Block
		stack       string
		history     string
		initialized string
	}
	ids := make(map[*cleanupRegion]int)
	tokens := make(map[*cleanupRegion]string)
	for i, r := range f.cleanups {
		ids[r] = i
		tokens[r] = "/" + strconv.Itoa(i)
	}
	// Immutable byte strings keep the complete per-identity sets comparable in
	// the worklist, without a bitmask limit on action or binding count.
	set := func(bits string, index int, value byte) string {
		out := []byte(bits)
		out[index] = value
		return string(out)
	}
	first := state{block: f.graph.Entry, history: string(make([]byte, len(f.cleanups))), initialized: string(make([]byte, len(f.bindings)))}
	seen, queue := map[state]bool{first: true}, []state{first}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		point := flow.points[current.block]
		for _, op := range current.block.Ops {
			if op.Kind == ssa.OpBindingInit {
				current.initialized = set(current.initialized, int(op.Imm)-1, 1)
			}
		}
		for _, r := range point.registers {
			id := ids[r]
			if current.history[id] != 0 {
				return false
			}
			current.history = set(current.history, id, 1)
			current.stack += tokens[r]
		}
		if r := point.replay; r != nil {
			id := ids[r]
			if current.history[id] != 0 {
				if !strings.HasSuffix(current.stack, tokens[r]) {
					return false
				}
				for _, capture := range r.captures {
					if current.initialized[capture-1] == 0 {
						return false
					}
				}
				current.stack = strings.TrimSuffix(current.stack, tokens[r])
			}
		}
		for _, end := range point.ends {
			for _, r := range f.cleanups {
				if r.boundary != end {
					continue
				}
				if strings.Contains(current.stack+"/", tokens[r]+"/") {
					return false
				}
				current.history = set(current.history, ids[r], 0)
			}
			for i, binding := range f.bindings {
				if binding.boundary == end {
					current.initialized = set(current.initialized, i, 0)
				}
			}
		}
		if current.block.Term.Kind == ssa.TermRet && (current.stack != "" || current.history != first.history) {
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
	// This permutation corpus has one scope. The nested corpus below uses source
	// CFGs; scope identity itself is tested through complete Verify calls.
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
				// This corpus supplies initializer instructions, not promoted
				// semantic records. Keep that phase and the payload explicit.
				f.unpromotedBindings = true
				payload := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
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
							block.Ops = append(block.Ops, &ssa.Op{Kind: ssa.OpBindingInit, Imm: 1, Args: []ssa.Value{payload}})
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

// Build the interpreter's event stream directly from the typed contract, not
// the production event index or scope/lifetime transfer helpers.
func cleanupOracleFlow(f *Func, registrations, replays int) *cleanupBoundaryFlow {
	flow := &cleanupBoundaryFlow{points: make(map[*ssa.Block]*cleanupFlowPoint)}
	for _, block := range f.graph.Blocks {
		flow.points[block] = &cleanupFlowPoint{}
	}
	for i, action := range f.cleanups {
		if registrations&(1<<i) != 0 {
			point := flow.points[action.register]
			point.registers = append(point.registers, action)
		}
		if replays&(1<<i) != 0 {
			for _, block := range action.replays {
				flow.points[block].replay = action
			}
		}
	}
	for _, exit := range f.cleanupExits {
		for _, end := range exit.ends {
			point := flow.points[end.block]
			point.ends = append(point.ends, end.boundary)
		}
	}
	return flow
}

func TestNestedCleanupCorrelationMatchesFullStackOracle(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"outer-pending-inner-close", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; }
  loop { var ys: i32[] = [3]; if (other) { defer ys = [4]; } break; }`},
		{"nested-iteration", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; }
  var i = 0i32; while (i < 3i32) { var ys: i32[] = [3];
    if (other) { defer ys = [4]; defer xs = [5]; } i = i + 1i32; }`},
		{"labelled-break", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; }
  outer: loop { var ys: i32[] = [3]; if (other) { defer ys = [4]; }
    loop { var zs: i32[] = [5]; if (flag) { defer zs = [6]; } break outer; } }`},
		{"continue", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; }
  var i = 0i32; while (i < 3i32) { var ys: i32[] = [3]; i = i + 1i32;
    if (other) { defer ys = [4]; } if (flag) { continue; } }`},
		{"return", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; }
  loop { var ys: i32[] = [3]; if (other) { defer ys = [4]; }
    if (flag) { return; } break; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := unverifiedCleanupSource(t, `function pilot(flag: boolean, other: boolean): void {`+tc.body+`}`)
			if err := Verify(f); err != nil {
				t.Fatal(err)
			}
			actionSets := 1 << len(f.cleanups)
			full := cleanupOracleFlow(f, actionSets-1, actionSets-1)
			if !cleanupStackOracle(f, full) {
				t.Fatal("full-stack oracle rejected verified nested source")
			}
			// Exhaustively delete registration, replay and captured-place init
			// events. These mutations compare trace admission, not structural SSA
			// or scope validation; the intact source passed complete Verify above.
			var captures []BindingID
			for _, action := range f.cleanups {
				for _, id := range action.captures {
					if !slices.Contains(captures, id) {
						captures = append(captures, id)
					}
				}
			}
			original := make(map[*ssa.Block][]*ssa.Op)
			for _, block := range f.graph.Blocks {
				original[block] = block.Ops
			}
			cases, accepted, rejected := 0, 0, 0
			for registrations := range actionSets {
				for replays := range actionSets {
					flow := cleanupOracleFlow(f, registrations, replays)
					for initialized := range 1 << len(captures) {
						for _, block := range f.graph.Blocks {
							block.Ops = slices.DeleteFunc(slices.Clone(original[block]), func(op *ssa.Op) bool {
								index := slices.Index(captures, BindingID(op.Imm))
								return op.Kind == ssa.OpBindingInit && index >= 0 && initialized&(1<<index) == 0
							})
						}
						want := cleanupStackOracle(f, flow)
						err := verifyCleanupProjections(f, flow)
						if (err == nil) != want {
							t.Fatalf("registration=%b replay=%b init=%b: projected=%v oracle=%v", registrations, replays, initialized, err, want)
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
			for block, ops := range original {
				block.Ops = ops
			}
			if accepted == 0 || rejected == 0 {
				t.Fatal("nested corpus must exercise acceptance and rejection")
			}
			t.Logf("%d exact nested comparisons: %d accepted, %d rejected", cases, accepted, rejected)
		})
	}
}
