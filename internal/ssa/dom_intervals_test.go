package ssa

import (
	"fmt"
	"sync"
	"testing"
)

// Deleting a candidate block disconnects exactly the reachable blocks that it
// dominates. This oracle uses CFG reachability, not idoms, RPO or DFS intervals.
func reachableWithout(entry, removed *Block) map[*Block]bool {
	seen := make(map[*Block]bool)
	queue := []*Block{entry}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if block == nil || block == removed || seen[block] {
			continue
		}
		seen[block] = true
		queue = append(queue, block.Succs()...)
	}
	return seen
}

func TestDomIntervalsMatchDeletionOracle(t *testing.T) {
	// Exhaust all four-block CFGs whose blocks return or have one/two distinct
	// successors. Includes arbitrary self/back edges, irreducible loops, dead
	// predecessor cycles and joins; there is no execution-depth bound.
	type edge struct{ a, b int }
	edges := []edge{{-1, -1}}
	for a := range 4 {
		edges = append(edges, edge{a, -1})
		for b := a + 1; b < 4; b++ {
			edges = append(edges, edge{a, b})
		}
	}
	graphs, pairs := 0, 0
	for code := 0; code < len(edges)*len(edges)*len(edges)*len(edges); code++ {
		f := NewFunc("oracle")
		cond := f.AddParam()
		for range 4 {
			f.NewBlock()
		}
		remaining := code
		for _, block := range f.Blocks {
			e := edges[remaining%len(edges)]
			remaining /= len(edges)
			switch {
			case e.a < 0:
				f.SetRet(block, Value{})
			case e.b < 0:
				f.SetBr(block, f.Blocks[e.a])
			default:
				f.SetBrIf(block, cond, f.Blocks[e.a], f.Blocks[e.b])
			}
		}
		dom := BuildDomTree(f)
		reachable := reachableWithout(f.Entry, nil)
		for _, a := range f.Blocks {
			without := reachableWithout(f.Entry, a)
			for _, b := range f.Blocks {
				want := a == b || (reachable[b] && !without[b])
				if got := dom.Dominates(a, b); got != want {
					t.Fatalf("graph=%d a=%d b=%d: got %v, want %v", code, a.ID, b.ID, got, want)
				}
				pairs++
			}
		}
		graphs++
	}
	t.Logf("%d CFGs, %d dominance comparisons against block deletion", graphs, pairs)
}

func dominanceChain(count int) *Func {
	f := NewFunc("chain")
	for range count {
		f.NewBlock()
	}
	for i, block := range f.Blocks {
		if i+1 == count {
			f.SetRet(block, Value{})
		} else {
			f.SetBr(block, f.Blocks[i+1])
		}
	}
	return f
}

func TestDomIntervalsDeepAndConcurrent(t *testing.T) {
	f := dominanceChain(4096)
	dom := BuildDomTree(f)
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for i := worker; i < len(f.Blocks); i += 8 {
				a, b := f.Blocks[i], f.Blocks[len(f.Blocks)-1-i]
				if dom.Dominates(a, b) != (i <= len(f.Blocks)-1-i) {
					t.Errorf("incorrect chain dominance at %d", i)
				}
			}
		})
	}
	workers.Wait()
}

func TestDomIntervalsNilDeadAndForeign(t *testing.T) {
	f := dominanceChain(2)
	dead := f.NewBlock()
	f.SetBr(dead, dead)
	foreign := dominanceChain(2)
	dom := BuildDomTree(f)
	for _, tc := range []struct {
		a, b *Block
		want bool
	}{
		{nil, nil, false}, {nil, f.Entry, false}, {f.Entry, nil, false},
		{dead, dead, true}, {dead, f.Entry, false}, {f.Entry, dead, false},
		{foreign.Entry, foreign.Entry, true},
		{foreign.Entry, f.Entry, false}, {f.Entry, foreign.Entry, false},
		{foreign.Entry, foreign.Blocks[1], false},
	} {
		if got := dom.Dominates(tc.a, tc.b); got != tc.want {
			t.Errorf("Dominates(%p,%p)=%v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	for _, empty := range []*DomTree{BuildDomTree(nil), BuildDomTree(NewFunc("empty")), {}} {
		if !empty.Dominates(dead, dead) || empty.Dominates(f.Entry, dead) {
			t.Fatal("empty tree changed non-nil reflexivity or invented dominance")
		}
	}
}

func TestVerifyWithDomTree(t *testing.T) {
	f := dominanceChain(3)
	dom, err := VerifyWithDomTree(f)
	if err != nil || dom == nil || !dom.Dominates(f.Entry, f.Blocks[2]) {
		t.Fatalf("valid graph lost its verified dominance snapshot: dom=%v err=%v", dom, err)
	}
	// Trigger a use-dominance failure after the verifier has built its tree.
	value := f.AddOp(f.Blocks[2], OpConstInt)
	f.AddOp(f.Entry, OpAdd, value, value)
	if dom, err := VerifyWithDomTree(f); err == nil || dom != nil {
		t.Fatalf("failed verification leaked an analysis snapshot: dom=%v err=%v", dom, err)
	}
	if dom, err := VerifyWithDomTree(nil); err == nil || dom != nil {
		t.Fatalf("nil graph: dom=%v err=%v", dom, err)
	}
	if dom, err := VerifyWithDomTree(NewFunc("empty")); err != nil || dom != nil {
		t.Fatalf("empty graph should have no analysis: dom=%v err=%v", dom, err)
	}
}

func BenchmarkDomIntervals(b *testing.B) {
	for _, count := range []int{2, 64, 1024} {
		f := dominanceChain(count)
		last := f.Blocks[count-1]
		b.Run(fmt.Sprintf("build-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if BuildDomTree(f).Idom[last] == nil {
					b.Fatal("missing dominator")
				}
			}
		})
		dom := BuildDomTree(f)
		b.Run(fmt.Sprintf("query-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if !dom.Dominates(f.Entry, last) {
					b.Fatal("missing dominance")
				}
			}
		})
	}
}
