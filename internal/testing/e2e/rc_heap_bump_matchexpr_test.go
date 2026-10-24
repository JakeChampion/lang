package e2e

import (
	"testing"
)

func matchExprBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "hello there "; let b: string = "world friend ";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) {
        let s: string = match (i % 2) { 0 => a + b, _ => b + a };
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const matchExprFreshCheck = `function main(): i32 {
    let a: string = "hello there "; let b: string = "world friend ";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let s: string = match (i % 2) { 0 => a + b, _ => b + a };
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 5000) { return 99; }
    return __rc_underflow_count();
}`

const matchExprAliasedSafe = `function main(): i32 {
    let a: string = "hello there friend ";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let s: string = match (i % 2) { 0 => a, _ => a };
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
