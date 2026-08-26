package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// f(c) = c != 0 ? 10 : 20  — a diamond with a merge phi. Exercises BrIf, the
// phi-move-to-split-block path, and the merge.
func TestEmitDiamondPhi(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("sel")
		c := f.AddParam()
		entry := f.NewBlock()
		thenB := f.NewBlock()
		elseB := f.NewBlock()
		merge := f.NewBlock()
		f.SetBrIf(entry, c, thenB, elseB)
		ten := constOp(f, thenB, 10)
		f.SetBr(thenB, merge)
		twenty := constOp(f, elseB, 20)
		f.SetBr(elseB, merge)
		p := f.AddPhi(merge, ten, twenty)
		f.SetRet(merge, p)
		return f
	}
	differential(t, build, [][]int64{{0}, {1}, {7}, {-1}})
}

// i = 0; while (i < n) { i = i + 1 } return i  ->  max(n, 0). Exercises a header
// phi resolved across both the entry edge and the back-edge.
func TestEmitCountingLoop(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("countTo")
		n := f.AddParam()
		entry := f.NewBlock()
		header := f.NewBlock()
		body := f.NewBlock()
		exit := f.NewBlock()

		init := constOp(f, entry, 0)
		f.SetBr(entry, header)

		inext := f.NewValue()
		i := f.AddPhi(header, init, inext)
		cond := f.AddOp(header, ssa.OpLt, i, n)
		f.SetBrIf(header, cond, body, exit)

		one := constOp(f, body, 1)
		add := f.AddOpNoResult(body, ssa.OpAdd, i, one)
		add.Result = inext
		f.SetBr(body, header)

		f.SetRet(exit, i)
		return f
	}
	differential(t, build, [][]int64{{0}, {1}, {5}, {10}, {-3}})
}

// A header phi whose two incoming values must be SWAPPED across the back-edge:
//
//	a, b = b, a   each iteration. The phi-move sequentialisation must preserve
//
// both, which means detecting that neither move can go first and parking one
// value before the cycle unrolls (a naive sequential copy clobbers one). After
// n iterations return a. Validates parallel-copy correctness on a real cycle.
func TestEmitPhiSwapCycle(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("swap")
		n := f.AddParam()
		entry := f.NewBlock()
		header := f.NewBlock()
		body := f.NewBlock()
		exit := f.NewBlock()

		a0 := constOp(f, entry, 100)
		b0 := constOp(f, entry, 200)
		i0 := constOp(f, entry, 0)
		f.SetBr(entry, header)

		// Pre-mint the back-edge values so the phis can reference them.
		aNext := f.NewValue()
		bNext := f.NewValue()
		iNext := f.NewValue()
		// header.Preds == [entry, body]; phis take (entry-val, body-val).
		a := f.AddPhi(header, a0, aNext)
		b := f.AddPhi(header, b0, bNext)
		i := f.AddPhi(header, i0, iNext)
		cond := f.AddOp(header, ssa.OpLt, i, n)
		f.SetBrIf(header, cond, body, exit)

		// body: swap (aNext=b, bNext=a), iNext=i+1.
		// aNext/bNext are just the existing a/b values flowing back swapped,
		// realised purely by how the back-edge phi args are wired below.
		one := constOp(f, body, 1)
		add := f.AddOpNoResult(body, ssa.OpAdd, i, one)
		add.Result = iNext
		f.SetBr(body, header)
		// Wire the swap: on the back-edge, a's phi pulls b and b's phi pulls a.
		// AddPhi already fixed the arg order, so rebind the back-edge args.
		header.Ops[0].Args[1] = b // a's phi back-edge value = b
		header.Ops[1].Args[1] = a // b's phi back-edge value = a
		_ = aNext
		_ = bNext

		f.SetRet(exit, a)
		return f
	}
	// After n swaps: a == 100 if n even, 200 if n odd.
	differential(t, build, [][]int64{{0}, {1}, {2}, {3}, {6}, {7}})
}

// A critical edge: entry --(brif)--> merge has merge with two preds, and entry
// has two succs, so the phi move for that edge must go on a split block, not in
// entry (which also branches elsewhere) nor merge (other pred). f(c) = c ? 1 : (phi).
func TestEmitCriticalEdge(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("crit")
		c := f.AddParam()
		entry := f.NewBlock()
		mid := f.NewBlock()
		merge := f.NewBlock()

		// entry: if c goto merge (with phi val 7) else goto mid
		seven := constOp(f, entry, 7)
		f.SetBrIf(entry, c, merge, mid)
		// mid: goto merge (with phi val 9)
		nine := constOp(f, mid, 9)
		f.SetBr(mid, merge)
		// merge: preds [entry, mid]; entry→merge is the critical edge.
		p := f.AddPhi(merge, seven, nine)
		f.SetRet(merge, p)
		return f
	}
	differential(t, build, [][]int64{{1}, {0}, {-5}})
}
