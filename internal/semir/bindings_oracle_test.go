package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// This oracle explores exact (block, initialized) states. It neither uses
// dominance nor bounds the number of loop iterations; two states per block
// suffice for the single-binding contract under test.
func bindingInitializationOracle(f *Func) bool {
	type state struct {
		block       *ssa.Block
		initialized bool
	}
	queue := []state{{block: f.graph.Entry}}
	seen := map[state]bool{queue[0]: true}
	for next := 0; next < len(queue); next++ {
		at := queue[next]
		for _, op := range at.block.Ops {
			switch op.Kind {
			case ssa.OpBindingInit:
				at.initialized = true
			case ssa.OpBindingRead, ssa.OpBindingReplace:
				if !at.initialized {
					return false
				}
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
	return true
}

func TestBindingInitializationMatchesExactCFGOracle(t *testing.T) {
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
				for _, readFirst := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/init%d/read%d/first%v", shape.name, initBlock, readBlock, readFirst), func(t *testing.T) {
						f, entry, id, value := bindingTestFunc()
						flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
						blocks := []*ssa.Block{entry}
						for len(blocks) < len(shape.edges) {
							blocks = append(blocks, f.graph.NewBlock())
						}
						if readFirst {
							f.readBinding(blocks[readBlock], id, ast.Position{})
						}
						f.writeBinding(blocks[initBlock], ssa.OpBindingInit, id, value, ast.Position{})
						if !readFirst {
							f.readBinding(blocks[readBlock], id, ast.Position{})
						}
						for i, edges := range shape.edges {
							switch {
							case edges[0] < 0:
								f.graph.SetRet(blocks[i], value)
							case edges[0] == edges[1]:
								f.graph.SetBr(blocks[i], blocks[edges[0]])
							default:
								f.graph.SetBrIf(blocks[i], flag, blocks[edges[0]], blocks[edges[1]])
							}
						}
						if err := ssa.Verify(f.graph); err != nil {
							t.Fatal(err)
						}
						want := bindingInitializationOracle(f)
						err := Verify(f)
						if (err == nil) != want {
							t.Fatalf("oracle=%v, verifier=%v", want, err)
						}
						if want {
							if err := promoteBindings(f); err != nil {
								t.Fatal(err)
							}
							if err := Verify(f); err != nil {
								t.Fatal(err)
							}
						}
					})
				}
			}
		}
	}
}

func BenchmarkVerifyBindingInitialization(b *testing.B) {
	for _, count := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("bindings-blocks-%d", count), func(b *testing.B) {
			f := newFunc("binding_scale", ast.NumberType{})
			f.unpromotedBindings = true
			entry := f.graph.NewBlock()
			value := f.addOp(entry, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
			previous := entry
			for i := 0; i < count; i++ {
				id := f.addBinding("item", ast.NumberType{}, ast.Position{})
				f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
				next := f.graph.NewBlock()
				f.graph.SetBr(previous, next)
				f.readBinding(next, id, ast.Position{})
				previous = next
			}
			f.graph.SetRet(previous, value)
			if err := Verify(f); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := Verify(f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
