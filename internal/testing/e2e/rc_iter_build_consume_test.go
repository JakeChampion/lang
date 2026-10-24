package e2e

import (
	"testing"
)

const iterBuildConsumeSrc = `enum List { Cons(i32, List), Nil }
function eat(own xs: List): i32 {
    match (xs) { Cons(h, t) => { return h + eat(t); }, Nil => { return 0; } }
}
function ib(n: i32): List {
    let acc: List = Nil;
    let i: i32 = 0;
    while (i < n) { acc = Cons(2, acc); i = i + 1; }   // n nodes, each value 2
    return acc;
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        total = total + eat(ib(20));   // 20 nodes * 2 = 40 per iter
        i = i + 1;
    }
    if (total != 2000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64IterBuildConsume(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, iterBuildConsumeSrc); code != 0 {
		t.Errorf("iter-build consumed: got %d, want 0", code)
	}
}

func TestArm64IterBuildConsume(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, iterBuildConsumeSrc); code != 0 {
		t.Errorf("iter-build consumed: got %d, want 0", code)
	}
}

func TestWASMIterBuildConsume(t *testing.T) {
	if got := runWasm(t, iterBuildConsumeSrc); got != 0 {
		t.Errorf("iter-build consumed: got %d, want 0", got)
	}
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function eat(own xs: List): i32 { match (xs) { Cons(h, t) => { return h + eat(t); }, Nil => { return 0; } } }
function ib(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(2, acc); i = i + 1; } return acc; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { let u: i32 = eat(ib(10)); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	if small, large := runWasm(t, bump("500")), runWasm(t, bump("5000")); small != large {
		t.Errorf("iter-build consume should be bounded: N=500 -> %d, N=5000 -> %d", small, large)
	}
}
