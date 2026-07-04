package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Regression for #4376: a string accumulator initialised from a bare string
// LITERAL (`var s = ""`) must be freeEligible so the dec-on-overwrite reclaims
// each intermediate. rhsTainted had *ast.NumberLit/FloatLit/BoolLit cases but
// NO *ast.StringLit case, so a plain `""` init fell to `default: return true`,
// tainted the local forever, and every reassignment leaked the prior buffer
// (O(n²) bump bytes on the hot `s = s + p` response-assembly pattern).
//
// This asserts at the IR layer — target-independently, so it covers every
// backend the shared analysis feeds — that the accumulator's overwrite emits a
// string dec. `p` is a borrowed parameter (never eligible, no exit-sweep dec),
// so the only string dec that can appear in `acc` is `s`'s: with the fix it is
// present (str_dec on the two-word ptrW=4 ABI, rc_dec on the single-word ptrW=8
// native ABI); without it, `acc` emits ZERO string decs at either width. The
// end-to-end heap-bump payoff is pinned separately in the e2e suite
// (TestWASMHeapBumpStrLiteralAccumBounded).
func TestLowerStrLiteralAccumEmitsOverwriteDec(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	// `p` is a borrowed param (not reclaimed here); `s` is the literal-init
	// accumulator overwritten with a fresh constant-size concat each iteration.
	src := `function acc(p: string): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 3) { s = p + "!"; i = i + 1; }
    return i;
}
function main(): i32 { return acc("hi"); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		decs := countStringDecs(prog, "acc")
		if decs == 0 {
			t.Errorf("ptrW=%d: literal-init string accumulator emitted no string dec in acc (freeEligible regressed — the `var s = \"\"` init is RC-tainted again, leaking every intermediate)", ptrW)
		}
	}
}

// countStringDecs counts the __fern_str_dec / __fern_rc_dec direct calls in a
// function — the string-reclaim ops (str_dec on two-word ABIs, rc_dec on the
// native single-word overwrite path).
func countStringDecs(p *Program, fnName string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if (op.Kind == OpCallDirect && op.Str == "__fern_str_dec") || op.Kind == OpRcDec {
				n++
			}
		}
	}
	return n
}
