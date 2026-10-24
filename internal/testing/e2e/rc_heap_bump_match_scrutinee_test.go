package e2e

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func matchScrutineeExprBumpSrc(n string) string {
	return `enum E3 { A(i32), B(i32), C }
function mk(v: i32): E3 { return A(v); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let r: i32 = match (mk(i)) { A(x) => x, B(y) => y, C => 0 };
        acc = acc + r;
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func matchScrutineeStmtBumpSrc(n string) string {
	return `enum E3 { A(i32), B(i32), C }
function mk(v: i32): E3 { return A(v); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (mk(i)) {
            A(x) => { acc = acc + x; },
            B(y) => { acc = acc + y; },
            C => {},
        }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func matchScrutineeLiteralBumpSrc(n string) string {
	return `enum E3 { A(i32), B(i32), C }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let r: i32 = match (A(i)) { A(x) => x, B(y) => y, C => 0 };
        match (B(i)) { A(x) => { acc = acc + x; }, B(y) => { acc = acc + y; }, C => {}, }
        acc = acc + r;
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const matchScrutineeLiteralAliasedPayloadSafe = `enum S3 { P(string), Q(i32), R }
function main(): i32 {
    let keep: string = "abc" + "def";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let r: i32 = match (P(keep)) { P(s) => s.len(), Q(y) => y, R => 0 };
        acc = acc + r;
        i = i + 1;
    }
    if (acc != 1200) { return 99; }
    if (keep.len() != 6) { return 88; }
    return __rc_underflow_count();
}`

const matchScrutineeAliasedSafe = `enum E3 { A(i32), B(i32), C }
@noinline function pass(b: E3): E3 { return b; }
function main(): i32 {
    let b: E3 = A(7);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let r: i32 = match (pass(b)) { A(x) => x, B(y) => y, C => 0 };
        acc = acc + r;
        i = i + 1;
    }
    let final: i32 = match (b) { A(x) => x, B(y) => y, C => 0 };
    if (acc != 1400) { return 99; }
    if (final != 7) { return 88; }
    return __rc_underflow_count();
}`

func checkMatchScrutineeSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, matchScrutineeAliasedSafe); code != 0 {
		t.Errorf("aliased-scrutinee safety: code=%d (99=value, 88=UAF/freed-early, >0=over-release)", code)
	}
	if _, code := run(t, matchScrutineeLiteralAliasedPayloadSafe); code != 0 {
		t.Errorf("literal aliased-payload safety: code=%d (99=value, 88=payload freed under caller, >0=over-release)", code)
	}
}

func TestX86_64MatchScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{matchScrutineeExprBumpSrc, matchScrutineeStmtBumpSrc, matchScrutineeLiteralBumpSrc} {
		small := mustRunX86_64FreeOn(t, mk("50"))
		large := mustRunX86_64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("match-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkMatchScrutineeSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64MatchScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{matchScrutineeExprBumpSrc, matchScrutineeStmtBumpSrc, matchScrutineeLiteralBumpSrc} {
		small := mustRunArm64FreeOn(t, mk("50"))
		large := mustRunArm64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("match-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkMatchScrutineeSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMMatchScrutineeReclaim(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, mk := range []func(string) string{matchScrutineeExprBumpSrc, matchScrutineeStmtBumpSrc, matchScrutineeLiteralBumpSrc} {
		small := runWasm(t, mk("50"))
		large := runWasm(t, mk("5000"))
		if small != large {
			t.Errorf("match-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
		}
	}
	checkMatchScrutineeSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
