package e2e

import (
	"testing"
)

const ownMapSrc = `enum List { Cons(i32, List), Nil }
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
        total = total + sum(map_inc(build(5)));   // map+1 over [5..1] then sum = 20
        i = i + 1;
    }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64OwnConsumingMatch(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownMapSrc); code != 0 {
		t.Errorf("recursive map_inc: got %d, want 0", code)
	}
}

func TestArm64OwnConsumingMatch(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownMapSrc); code != 0 {
		t.Errorf("recursive map_inc: got %d, want 0", code)
	}
}

func TestWASMOwnConsumingMatch(t *testing.T) {
	if got := runWasm(t, ownMapSrc); got != 0 {
		t.Errorf("recursive map_inc: got %d, want 0", got)
	}
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function map_inc(own xs: List): List { match (xs) { Cons(h,t) => { return Cons(h+1, map_inc(t)); }, Nil => { return Nil; } } }
function sum(l: List): i32 { match (l) { Cons(h,t) => { return h + sum(t); }, Nil => { return 0; } } }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) {
        let unused: i32 = sum(map_inc(build(8)));
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bump("2000"))
	large := runWasm(t, bump("20000"))
	if small != large {
		t.Errorf("consuming map should be bounded (cells recycled): N=2000 -> %d, N=20000 -> %d", small, large)
	}
}

const ownTagOnlyThenMatchSrc = `enum Box { Str(string), Arr(i32[]), Nil }

@noinline function tag_only(own b: Box): i32 {
    let t: i32 = 0;
    match (b) { Str(_) => { t = 1; }, Arr(_) => { t = 2; }, Nil => { t = 3; } }
    match (b) { Str(s) => { return t * 100 + s.len(); }, Arr(a) => { return t * 100 + a.len(); }, Nil => { return t * 100; } }
}

function main(): i32 {
    let v: i32 = tag_only(Str("abcde")) + tag_only(Arr([1, 2, 3])) + tag_only(Nil);
    if (__rc_underflow_count() != 0) { return 99; }
    return v % 256;
}`

func TestX86_64OwnTagOnlyMatchThenMatch(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownTagOnlyThenMatchSrc); code != 96 {
		t.Errorf("tag-only match then a payload match: got %d, want 96", code)
	}
}

func TestArm64OwnTagOnlyMatchThenMatch(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownTagOnlyThenMatchSrc); code != 96 {
		t.Errorf("tag-only match then a payload match: got %d, want 96", code)
	}
}

func TestWASMOwnTagOnlyMatchThenMatch(t *testing.T) {
	if got := runWasm(t, ownTagOnlyThenMatchSrc); got != 96 {
		t.Errorf("tag-only match then a payload match: got %d, want 96", got)
	}
}
