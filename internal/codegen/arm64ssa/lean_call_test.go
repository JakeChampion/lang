package arm64ssa_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// A call site saves the values the allocator says are live across it, narrowed
// to the ones its callee can actually disturb (helperClobbers, pinned by
// lean_call_internal_test.go). These two check the emitted effect: that the
// saves go away, and that the values still come back.

// The narrowing itself, measured against the callee it cannot narrow: the same
// module calling a compiled function of the same arity keeps every live-across
// value in the call-save area, and calling an rc primitive does not. Stated as
// a comparison because a function's prologue also writes sp-relative slots, and
// those are not what this is about.
func TestLeanHelperCallDropsTheCallerSaves(t *testing.T) {
	// A file small enough that every home is caller-saved: at the default size
	// the allocator steers a call-crossing value into x19..x28 instead, so
	// neither call has a save area to compare.
	spStores := func(funcs map[string]*ssa.Func) int {
		asm, err := arm64ssa.EmitAsmModule(funcs, "main", 6, nil)
		if err != nil {
			t.Fatalf("EmitAsmModule: %v", err)
		}
		return len(regexp.MustCompile(`(?m)^\s*(str|stp)\s+x\d+.*\[sp`).FindAllString(funcBody(asm, "fn_main"), -1))
	}
	lean := spStores(liveAcrossRcModule())
	opaque := spStores(liveAcrossOpaqueModule())
	if lean >= opaque {
		t.Errorf("a lean helper call saves as much as an opaque one: %d vs %d", lean, opaque)
	}
}

// And it stays correct: the live values have to come back out of those registers
// unchanged, which is the whole thing the saves were there for.
func TestValuesLiveAcrossRcCallsSurvive(t *testing.T) {
	for _, n := range []int{4, 8, arm64ssa.DefaultNumAlloc} {
		if got := assembleRunArmModule(t, liveAcrossRcModule(), "main", n); got != 26 {
			t.Errorf("nAlloc=%d live-across-rc = %d, want 26", n, got)
		}
	}
}

// funcBody returns the emitted text of one function, up to the next one.
func funcBody(asm, label string) string {
	i := strings.Index(asm, "\n"+label+":")
	if i < 0 {
		return ""
	}
	rest := asm[i+1:]
	if j := strings.Index(rest, "\nfn_"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// liveAcrossOpaqueModule is liveAcrossRcModule with the rc calls replaced by
// calls to a compiled function, whose clobbers this file cannot know.
func liveAcrossOpaqueModule() map[string]*ssa.Func {
	funcs := liveAcrossRcModule()
	for _, blk := range funcs["main"].Blocks {
		for _, op := range blk.Ops {
			switch op.Str {
			case "__fern_rc_inc":
				op.Str = "opaque_inc"
			case "__fern_rc_is_unique":
				op.Str = "opaque_uniq"
			}
		}
	}
	for _, name := range []string{"opaque_inc", "opaque_uniq"} {
		g := ssa.NewFunc(name)
		p := g.AddParam()
		e := g.NewBlock()
		g.SetRet(e, p)
		funcs[name] = g
	}
	return funcs
}

// liveAcrossRcModule: four values kept live across an rc_inc and an is_unique on
// a heap cell. 3+5+7+11 = 26, and is_unique reads 0 because the inc took the
// count to 2 — so a clobbered live value or a missed inc both change the answer.
func liveAcrossRcModule() map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	cell := rcCell(f, b, 8)
	a := constOp(f, b, 3)
	c := constOp(f, b, 5)
	d := constOp(f, b, 7)
	e := constOp(f, b, 11)
	addrCallOp(f, b, "__fern_rc_inc", cell)
	uniq := callOp(f, b, "__fern_rc_is_unique", cell)
	sum := f.AddOp(b, ssa.OpAdd, a, f.AddOp(b, ssa.OpAdd, c, f.AddOp(b, ssa.OpAdd, d, e)))
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, sum, f.AddOp(b, ssa.OpMul, uniq, constOp(f, b, 100))))
	return map[string]*ssa.Func{"main": f}
}
