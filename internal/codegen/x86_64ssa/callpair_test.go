package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// callPairOp adds an OpCallPair to callee and returns its two results.
func callPairOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) (ssa.Value, ssa.Value) {
	tag, payload := f.AddCallPair(b, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return tag, payload
}

// split(x) returns the pair (x, x+100); main() sums the two results. Validates
// OpCallPair (two-result direct call) + TermRetPair against EvalIn, incl. under
// spill-forcing register counts.
func TestModuleCallPair(t *testing.T) {
	split := ssa.NewFunc("split")
	x := split.AddParam()
	se := split.NewBlock()
	hi := split.AddOp(se, ssa.OpAdd, x, constOp(split, se, 100))
	split.SetRetPair(se, x, hi)

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	tag, payload := callPairOp(main, me, "split", constOp(main, me, 5))
	main.SetRet(me, main.AddOp(me, ssa.OpAdd, tag, payload))

	funcs := map[string]*ssa.Func{"split": split, "main": main}
	moduleMatchesEval(t, funcs, "main", [][]int64{{}})
}

// A pair-returning function keeps both results live across an intervening call,
// forcing them apart from the scratch registers and (at nAlloc=1) through slots.
func TestModuleCallPairBothResultsLive(t *testing.T) {
	// id(v) = v — an intervening call between the pair call and the use, so the
	// tag/payload must survive a callee-clobbering point.
	id := ssa.NewFunc("id")
	iv := id.AddParam()
	ie := id.NewBlock()
	id.SetRet(ie, iv)

	split := ssa.NewFunc("split")
	x := split.AddParam()
	se := split.NewBlock()
	hi := split.AddOp(se, ssa.OpMul, x, constOp(split, se, 3))
	split.SetRetPair(se, x, hi)

	main := ssa.NewFunc("main")
	p := main.AddParam()
	me := main.NewBlock()
	tag, payload := callPairOp(main, me, "split", p)
	mid := callOp(main, me, "id", tag) // intervening call
	sum := main.AddOp(me, ssa.OpAdd, mid, payload)
	main.SetRet(me, sum)

	funcs := map[string]*ssa.Func{"id": id, "split": split, "main": main}
	moduleMatchesEval(t, funcs, "main", [][]int64{{4}, {10}, {0}})
}
