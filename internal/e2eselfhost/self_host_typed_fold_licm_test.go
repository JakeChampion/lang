package e2eselfhost

import (
	"testing"
)

// The self-hosted CLI on its default (typed) lowering, which routes every
// intermediate through a slot. The CLI folds literal sub-expressions in the
// AST first, so these programs reach their constants through an inlined leaf's
// parameter: only the IR passes can see them.

// typedFoldCases fold only when propagation and folding run to a fixpoint:
// each fold stores a new constant that the next propagation has to carry to
// its load. The interpreter is the oracle, and every case returns <= 120.
var typedFoldCases = []struct {
	name string
	main string
}{
	{"nested", `function f(x: i32): i32 { return (x * 2 + 1) * 3; } function main(): i32 { return f(3) + 1; }`},
	{"chain", `function f(x: i32): i32 { var b: i32 = x * 2; var c: i32 = b + 1; return c * c - b; } function main(): i32 { return f(3); }`},
	{"i32-wraps-at-32-bits", `function f(x: i32): i32 { return (x + 1) + 0; } function main(): i32 { if (f(2147483647) < 0) { return 11; } return 1; }`},
	{"u8-masks", `function f(x: u8): u8 { return (x + (10 as u8)) * (3 as u8); } function main(): i32 { return (f(250 as u8) as i32) + 5; }`},
	{"u32-max-survives", `function f(x: u32): u32 { return (x + (1 as u32)) + (2 as u32); } function main(): i32 { return (f(4294967295 as u32) as i32) + 7; }`},
	{"i64-extend", `function f(x: i32): i64 { return ((x * 3 + 1) as i64) * (4 as i64); } function main(): i32 { return (f(3) as i32) + 1; }`},
	{"shl", `function f(x: i32): i32 { var a: i32 = x << 3; return (a << 2) + (a >> 1); } function main(): i32 { return f(1); }`},
}

func TestSelfHostTypedFoldValues(t *testing.T) {
	cli := newStrictCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range typedFoldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			if got, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); got != want {
				t.Errorf("x86-64 exited %d, want %d (interp oracle)", got, want)
			}
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", src)); got != want {
				t.Errorf("wasm exited %d, want %d (interp oracle)", got, want)
			}
		})
	}
}

// TestSelfHostTypedFoldShape pins that a constant expression built through
// slots reaches the backend as one constant: `f(3) + 1` inlines to three
// slot-routed operations over constants and must emit 22 with no arithmetic.
// The opaque twin passes a parameter instead, so it keeps the arithmetic this
// looks for and the absence in `folded` is not vacuous.
func TestSelfHostTypedFoldShape(t *testing.T) {
	cli := newStrictCLI(t)
	src := "function f(x: i32): i32 { return (x * 2 + 1) * 3; }\n" +
		"@noinline function folded(): i32 { return f(3) + 1; }\n" +
		"@noinline function opaque(y: i32): i32 { return f(y) + 1; }\n" +
		"function main(): i32 { return folded() - opaque(3); }\n"
	for _, tc := range []struct {
		target, want, arith string
	}{
		{"x86-64-linux", `movl \$22, %e[a-z0-9]+`, `(?m)^\s*(add|sub|imul|shl|sal|lea)[lq]? .*%(r[0-9]+[dwb]?|[re](ax|bx|cx|dx|si|di))$`},
		{"arm64-linux", `mov x[0-9]+, #22`, `(?m)^\s*(add|sub|mul|madd|lsl) [xw][0-9]+, `},
	} {
		t.Run(tc.target, func(t *testing.T) {
			asm := cli.emit(t, tc.target, src)
			folded := asmFuncBody(t, asm, "__fn_folded")
			opaque := asmFuncBody(t, asm, "__fn_opaque")
			if !matchShape(folded, tc.want) {
				t.Errorf("no %q for the inlined `f(3) + 1`:\n%s", tc.want, folded)
			}
			if matchShape(folded, tc.arith) {
				t.Errorf("arithmetic left in a wholly constant expression:\n%s", folded)
			}
			if !matchShape(opaque, tc.arith) {
				t.Errorf("the opaque twin has no arithmetic either, so the check above proves nothing:\n%s", opaque)
			}
		})
	}
}
