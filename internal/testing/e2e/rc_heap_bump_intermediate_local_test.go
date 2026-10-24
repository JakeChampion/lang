package e2e

import (
	"testing"
)

const intermediateLocalFlat = `struct St { xs: i32[], n: i32 }
function build(k: i32): St { return St { xs: [k, k + 1, k + 2], n: k }; }
function bump(s: St): St { return St { xs: [s.n + 1, 2, 3], n: s.n + 1 }; }
function f(s: St): i32 { let t: St = bump(s); let u: St = bump(t); return u.n + u.xs[0]; }
function churn(m: i32): i32 { let s: St = build(1); let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(2000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`

const intermediateLocalAliasedSafe = `struct St { xs: i32[], n: i32 }
function build(k: i32): St { return St { xs: [k, k + 1, k + 2], n: k }; }
@noinline function id(s: St): St { return s; }
function f(s: St): i32 { let t: St = id(s); let u: i32 = t.n + t.xs[0]; return u + s.xs[1]; }
function churn(m: i32): i32 { let s: St = build(1); let acc: i32 = 0; let i: i32 = 0; while (i < m) { acc = (acc + f(s)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(2000); let x: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } if (w != x) { return 97; } return 0; }`

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
	if got := runWasm(t, intermediateLocalFlat); got != 0 {
		t.Errorf("intermediate-local flat (wasm): code=%d (98=bump grew → t leaked; 99=over-release; 97=value)", got)
	}
	if got := runWasm(t, intermediateLocalAliasedSafe); got != 0 {
		t.Errorf("aliased intermediate safety (wasm): code=%d", got)
	}
}
