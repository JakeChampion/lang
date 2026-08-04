package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Dead NAMED intermediate consumed by a borrowing call (#4357). In
// `var t = f(x); var u = g(t); return u;` the intermediate t dies after g
// borrows it, but rhsTainted used to propagate the receiver/arg taint of f's
// inputs into t, leaving it permanently free-INeligible — the missing drop
// that dominates the self-compile RSS (docs/SELFHOST-BSTATE-RECLAIM-PLAN.md
// "The real leak": ~915 MB over 20M iterations on the BState probe). The fix
// consults findReturnsNoParamEscape in rhsTainted's Call case: a callee whose
// every return is built from scalars and fresh constructions (transitively —
// the same oracle the nested-call temp reclaim already trusts) hands back a
// fresh owned value regardless of input taint, so t stays freeEligible and
// gets its precise/exit drop.
//
// The reclaim is proven by an in-program bounded high-water check (a second
// churn of equal size must not move __heap_bump_bytes(); exit 98 on growth,
// 99 on over-release, 97 on a corrupted value); soundness by an
// alias-returning callee (id(s) returns its param — NOT in the oracle, so the
// intermediate stays tainted and nothing frees the caller's live struct).

const intermediateLocalFlat = `struct St { xs: i32[], n: i32 }
function build(k: i32): St { return St { xs: [k, k + 1, k + 2], n: k }; }
function bump(s: St): St { return St { xs: [s.n + 1, 2, 3], n: s.n + 1 }; }
function f(s: St): i32 { var t: St = bump(s); var u: St = bump(t); return u.n + u.xs[0]; }
function churn(m: i32): i32 { var s: St = build(1); var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(2000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`

const intermediateLocalAliasedSafe = `struct St { xs: i32[], n: i32 }
function build(k: i32): St { return St { xs: [k, k + 1, k + 2], n: k }; }
function id(s: St): St { return s; }
function f(s: St): i32 { var t: St = id(s); var u: i32 = t.n + t.xs[0]; return u + s.xs[1]; }
function churn(m: i32): i32 { var s: St = build(1); var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(2000); var x: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } return 0; }`

func TestX86_64IntermediateLocalReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, intermediateLocalFlat); code != 0 {
		t.Errorf("intermediate-local flat: code=%d (98=bump grew → t leaked; 99=over-release; 97=value)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, intermediateLocalAliasedSafe); code != 0 {
		t.Errorf("aliased intermediate safety: code=%d (99=over-release/UAF; 97=value)", code)
	}
}

func TestArm64IntermediateLocalReclaim(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, intermediateLocalFlat); code != 0 {
		t.Errorf("intermediate-local flat: code=%d (98=bump grew → t leaked; 99=over-release; 97=value)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, intermediateLocalAliasedSafe); code != 0 {
		t.Errorf("aliased intermediate safety: code=%d", code)
	}
}

func TestWASMIntermediateLocalReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, intermediateLocalFlat); got != 0 {
		t.Errorf("intermediate-local flat (wasm): code=%d (98=bump grew → t leaked; 99=over-release; 97=value)", got)
	}
	if got := runWasm(t, intermediateLocalAliasedSafe); got != 0 {
		t.Errorf("aliased intermediate safety (wasm): code=%d", got)
	}
}
