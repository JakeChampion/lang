package e2e

import (
	"testing"
)

const consumingMethodSrc = `enum List { Cons(i32, List), Nil }
function (own xs: List) inc(): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, t.inc()); },
        Nil => { return Nil; },
    }
}
function sum(l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } }
}
function build(n: i32): List {
    if (n == 0) { return Nil; }
    return Cons(n, build(n - 1));
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        total = total + sum(build(5).inc());   // [5..1] +1 each, sum = 20
        i = i + 1;
    }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ConsumingMethod(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, consumingMethodSrc); code != 0 {
		t.Errorf("consuming method: got %d, want 0", code)
	}
}

func TestArm64ConsumingMethod(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, consumingMethodSrc); code != 0 {
		t.Errorf("consuming method: got %d, want 0", code)
	}
}

func TestWASMConsumingMethod(t *testing.T) {
	if got := runWasm(t, consumingMethodSrc); got != 0 {
		t.Errorf("consuming method: got %d, want 0", got)
	}
}
