package e2e

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func callResultArgStructBump(n string) string {
	return `struct P { x: i32, y: i32 }
function mk(v: i32): P { return P { x: v, y: v }; }
function take(p: P): i32 { return p.x + p.y; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + take(mk(i)); i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func callResultArgArrBump(n string) string {
	return `function inner(v: i32): i32[] { return [v, v + 1, v + 2]; }
function outer(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + outer(inner(i)); i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const callResultArgAliasedSafe = `@noinline function pass(p: i32[]): i32[] { return p; }
function sum(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    let arr: i32[] = [10, 20, 30];
    while (i < 200) {
        acc = acc + sum(pass(arr));
        acc = acc + arr[0];
        i = i + 1;
    }
    if (acc != 14000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64CallResultArgReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, callResultArgAliasedSafe); code != 0 {
		t.Errorf("aliased call-result-arg safety: code=%d (999=value mismatch, >0=over-release/UAF)", code)
	}
}

func TestArm64CallResultArgReclaim(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, callResultArgAliasedSafe); code != 0 {
		t.Errorf("aliased call-result-arg safety: code=%d", code)
	}
}

func TestWASMCallResultArgReclaim(t *testing.T) {
	e2eharness.BoxedProbes(t)
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"struct-result", callResultArgStructBump},
		{"array-result", callResultArgArrBump},
	} {
		small := runWasm(t, c.src("50"))
		large := runWasm(t, c.src("5000"))
		if small != large {
			t.Errorf("%s call-result-arg bump should be bounded: N=50 -> %d, N=5000 -> %d", c.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", c.name)
		}
	}
	if got := runWasm(t, callResultArgAliasedSafe); got != 0 {
		t.Errorf("aliased call-result-arg safety: %d", got)
	}
}
