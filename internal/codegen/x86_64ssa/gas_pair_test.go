package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A pair-returning callee (TRetPair) reached via a two-result call (CallPair):
// split(x) returns (x, x+100); main sums them. Exercises the System V
// pair-return convention (tag=rax, payload=rdx) end to end in real asm.
func TestAsmRunPairReturn(t *testing.T) {
	build := func() map[string]*ssa.Func {
		split := ssa.NewFunc("split")
		x := split.AddParam()
		se := split.NewBlock()
		hi := split.AddOp(se, ssa.OpAdd, x, constOp(split, se, 100))
		split.SetRetPair(se, x, hi)

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		tag, pay := callPairOp(main, me, "split", constOp(main, me, 5))
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, tag, pay))
		return map[string]*ssa.Func{"split": split, "main": main}
	}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, build(), "main", n, nil) // 5 + 105 = 110
	}
}

// Both results kept live across an intervening call, so the pair-return capture
// (rax/rdx) and the caller-saved handling must both hold. main computes
// split(p) -> (tag, pay), then id(tag) + pay.
func TestAsmRunPairReturnLiveAcrossCall(t *testing.T) {
	build := func() map[string]*ssa.Func {
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
		tag, pay := callPairOp(main, me, "split", p)
		mid := callOp(main, me, "id", tag)
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, mid, pay))
		return map[string]*ssa.Func{"id": id, "split": split, "main": main}
	}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, build(), "main", n, []int64{7}) // 7 + 21 = 28
	}
}
