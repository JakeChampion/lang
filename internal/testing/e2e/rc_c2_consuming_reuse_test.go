package e2e

import (
	"testing"
)

const c2ConsumingSrc = `enum List { Cons(i32, List), Nil }
function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
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
        total = total + sum(map_inc(build(5)));   // [5..1] +1 each, sum = 20
        i = i + 1;
    }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64C2ConsumingReuse(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, c2ConsumingSrc); code != 0 {
		t.Errorf("C2 consuming reuse: got %d, want 0", code)
	}
}

func TestArm64C2ConsumingReuse(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, c2ConsumingSrc); code != 0 {
		t.Errorf("C2 consuming reuse: got %d, want 0", code)
	}
}

func TestWASMC2ConsumingReuse(t *testing.T) {
	if got := runWasm(t, c2ConsumingSrc); got != 0 {
		t.Errorf("C2 consuming reuse: got %d, want 0", got)
	}
}
