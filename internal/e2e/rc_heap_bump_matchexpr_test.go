package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Match-expression owned-result reclamation. `rhsTainted` classified an
// `IfExpr` by its arms but had no `MatchExpr` case, so it fell through to the
// tainted default: `var s = match (k) { 0 => a + b, _ => b + a }` (all arms
// fresh concats) was read as borrowed, left ineligible, and leaked the
// concat buffer every iteration (240000 -> 2400000 in a loop). Adding the
// MatchExpr case (owned iff every arm body is owned — the exact IfExpr
// mirror) reclaims it.
//
// The bare-local-arm alias case stays protected by the SAME mechanism IfExpr
// uses: computeFreeEligible's escape(arm.Body) taints a local yielded from an
// arm, so rhsTainted reads it back as tainted and the match-expr stays
// ineligible — never freeing a value the source still owns.

func matchExprBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var a: string = "hello there "; var b: string = "world friend ";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) {
        var s: string = match (i % 2) { 0 => a + b, _ => b + a };
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value + over-release for the all-fresh-arm case: a(12) + b(13) == 25 either
// way, x200 == 5000.
const matchExprFreshCheck = `function main(): i32 {
    var a: string = "hello there "; var b: string = "world friend ";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var s: string = match (i % 2) { 0 => a + b, _ => b + a };
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 5000) { return 99; }
    return __rc_underflow_count();
}`

// CRITICAL soundness: an arm returns a bare local (`a`, aliased). The
// match-expr must stay ineligible (escape-tainted), so reclaiming `s` never
// frees the still-live `a`. a == 19, s == a == 19, acc += s.len() + a.len()
// == 38 per iter, x200 == 7600.
const matchExprAliasedSafe = `function main(): i32 {
    var a: string = "hello there friend ";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var s: string = match (i % 2) { 0 => a, _ => a };
        acc = acc + s.len() + a.len();
        i = i + 1;
    }
    if (acc != 7600) { return 99; }
    return __rc_underflow_count();
}`

func checkMatchExpr(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, matchExprFreshCheck); code != 0 {
		t.Errorf("all-fresh-arm value/over-release: code=%d (99=value, >0=over-release)", code)
	}
	if _, code := run(t, matchExprAliasedSafe); code != 0 {
		t.Errorf("bare-local-arm safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
}

func TestX86_64MatchExprReclaim(t *testing.T) {
	checkMatchExpr(t, compileAndRunX86_64FreeOn)
}

func TestArm64MatchExprReclaim(t *testing.T) {
	checkMatchExpr(t, compileAndRunArm64FreeOn)
}

func TestWASMMatchExprReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, matchExprBumpSrc("5000"))
	large := runWasm(t, matchExprBumpSrc("50000"))
	if small != large {
		t.Errorf("match-expr bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	checkMatchExpr(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
