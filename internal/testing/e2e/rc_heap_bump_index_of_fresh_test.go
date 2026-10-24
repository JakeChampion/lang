package e2e

import (
	"testing"
)

func indexFreshBumpSrc(n string) string {
	return `function mk(v: i32): i32[] { return [v, v + 1, v + 2]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32 = mk(i)[1]; acc = acc + v; i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const indexFreshAliasedSafe = `@noinline function pass(p: i32[]): i32[] { return p; }
function main(): i32 {
    let arr: i32[] = [10, 20, 30];
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { let v: i32 = pass(arr)[1]; acc = acc + v + arr[0]; i = i + 1; }
    if (acc != 6000) { return 99; }
    return __rc_underflow_count();
}`

const indexFreshPtrElem = `function mks(): string[] { return ["alpha", "beta", "gamma"]; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { let s: string = mks()[0]; acc = acc + s.len(); i = i + 1; }
    if (acc != 1000) { return 99; }
    return __rc_underflow_count();
}`

func checkIndexFresh(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, indexFreshAliasedSafe); code != 0 {
		t.Errorf("aliased-container safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
	if _, code := run(t, indexFreshPtrElem); code != 0 {
		t.Errorf("pointer-element value/over-release: code=%d", code)
	}
}

func TestX86_64IndexOfFreshReclaim(t *testing.T) {
	checkIndexFresh(t, compileAndRunX86_64FreeOn)
}

func TestArm64IndexOfFreshReclaim(t *testing.T) {
	checkIndexFresh(t, compileAndRunArm64FreeOn)
}

func TestWASMIndexOfFreshReclaim(t *testing.T) {
	small := runWasm(t, indexFreshBumpSrc("5000"))
	large := runWasm(t, indexFreshBumpSrc("50000"))
	if small != large {
		t.Errorf("index-of-fresh bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	checkIndexFresh(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
