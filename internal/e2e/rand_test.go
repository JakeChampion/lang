package e2e

import "testing"

// Differential coverage for std/rand across backends. The draws are
// non-deterministic, so the program checks the invariants: shuffle
// preserves the multiset (sum + every element present) and leaves its
// input untouched; choice stays in-bounds (None only on empty); sample
// returns k distinct input elements. Returns 42 iff all hold. Each leg
// skips itself when its toolchain is absent.
const randProg = `
import "std/rand" as rand;
function sum(a: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }
function has(a: i32[], v: i32): boolean { var i: i32 = 0; while (i < a.len()) { if (a[i] == v) { return true; } i = i + 1; } return false; }
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
    var s: i32[] = rand.shuffle(xs);
    if (s.len() != 10 || sum(s) != 55) { return 1; }
    if (xs[0] != 1 || xs[9] != 10) { return 2; }            // input untouched
    var v: i32 = 1; while (v <= 10) { if (!has(s, v)) { return 3; } v = v + 1; }
    var c: i32 = 0;
    while (c < 40) { match (rand.choice(xs)) { Some(e) => { if (!has(xs, e)) { return 4; } }, None => { return 5; } } c = c + 1; }
    var empty: i32[] = [];
    match (rand.choice(empty)) { Some(e) => { return 6; }, None => {} }
    var samp: i32[] = rand.sample(xs, 4);
    if (samp.len() != 4) { return 7; }
    var a: i32 = 0;
    while (a < samp.len()) {
        if (!has(xs, samp[a])) { return 8; }
        var b: i32 = a + 1;
        while (b < samp.len()) { if (samp[a] == samp[b]) { return 9; } b = b + 1; }
        a = a + 1;
    }
    if (rand.sample(xs, 100).len() != 10 || rand.sample(xs, 0).len() != 0) { return 10; }
    return 42;
}
`

func TestRandInterp(t *testing.T) {
	if got := runInterpExit(t, randProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRandX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, randProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRandWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, randProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRandArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, randProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
