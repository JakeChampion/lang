package e2e

import "testing"

// `var s: S = src` where `src` is a PARAMETER is a borrow for the SEED and an
// owned value for every assignment after it. Ownership was one verdict per
// local, so the seed's verdict governed the whole slot and each later value it
// held was released with a plain dec that frees nothing — one leaked box per
// iteration, unbounded (#6403).
//
// The binding already emits the transfer inc (needsRcIncOnAlias holds for a
// pointer-shaped ident), so the local genuinely owns a reference and each of
// its drops is is_unique-gated; what was missing was crediting it with that.
//
// This file pins both halves: the ANSWERS on the shapes where a wrongly
// narrowed verdict would over-release (the caller's value read after the call,
// an alias taken before the reassignment, escapes via return and via a
// container, a conditional reassignment), and the BYTES, which is the half no
// correctness suite can see.

const rcParamSeedAnswerProg = `
import "std/i32";
import "std/string";

struct P { a: i32, b: i32 }

// The caller still owns src, and reads it after the call.
function seed_then_reassign(src: P, n: i32): i32 {
    var s: P = src;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { s = P { a: s.a + 1, b: s.b }; acc = acc + s.a; i = i + 1; }
    return acc;
}

// An alias taken from the seed and read after the seed's slot moved on.
function seed_alias(src: P): i32 {
    var s: P = src;
    var t: P = s;
    s = P { a: 99, b: 99 };
    return t.a + t.b + s.a;
}

// Escapes via return, on one path the seed survives.
function seed_return(src: P, c: boolean): P {
    var s: P = src;
    if (c) { s = P { a: 7, b: 7 }; }
    return s;
}

// Escapes into a container, on one path still holding the seed.
function seed_into_container(src: P, c: boolean): i32 {
    var s: P = src;
    if (c) { s = P { a: 5, b: 5 }; }
    var box: P[] = [s];
    return box[0].a + box[0].b;
}

function seed_cond(src: P, c: boolean): i32 {
    var s: P = src;
    if (c) { s = P { a: 3, b: 4 }; }
    return s.a * 10 + s.b;
}

function seed_array(src: i32[], n: i32): i32 {
    var s: i32[] = src;
    var i: i32 = 0;
    while (i < n) { s = [i, i + 1]; i = i + 1; }
    return s.len() + src.len();
}

function seed_string(src: string, n: i32): i32 {
    var s: string = src;
    var i: i32 = 0;
    while (i < n) { s = s + "x"; i = i + 1; }
    return s.len() + src.len();
}

function main(): i32 {
    var p: P = P { a: 1, b: 2 };
    if (seed_then_reassign(p, 50) != 1325) { return 1; }
    if (p.a * 100 + p.b != 102) { return 2; }
    if (seed_alias(p) != 102) { return 3; }
    var q: P = seed_return(p, false);
    var q2: P = seed_return(p, true);
    if (q.a * 1000 + q.b * 100 + q2.a * 10 + q2.b != 1277) { return 4; }
    if (seed_into_container(p, false) + seed_into_container(p, true) != 13) { return 5; }
    if (seed_cond(p, false) + seed_cond(p, true) != 46) { return 6; }
    var arr: i32[] = [8];
    if (seed_array(arr, 3) + arr.len() != 4) { return 7; }
    var s0: string = "ab";
    if (seed_string(s0, 4) + s0.len() != 10) { return 8; }
    if (p.a * 10 + p.b != 12) { return 9; }
    if (__rc_underflow_count() != 0) { return 10; }
    return 42;
}
`

func TestRcParamSeedAnswersInterp(t *testing.T) {
	if got := runInterpExit(t, rcParamSeedAnswerProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRcParamSeedAnswersX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, rcParamSeedAnswerProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRcParamSeedAnswersWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, rcParamSeedAnswerProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRcParamSeedAnswersArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, rcParamSeedAnswerProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

// The bytes. `step(state) -> (T, State)` with the loop factored into a helper
// is what puts the carried state on a parameter seed, and it is the ordinary
// way to write anything incremental.
//
// The work per round is CONSTANT, so the assertion is that doubling the rounds
// adds nothing rather than a growth ratio: measured 0 B after the fix and
// 6368 / 12768 B before it, on both arm64-darwin and wasm.
//
// __rc_underflow_count() rides along in the other direction — narrowing the
// verdict too far would release the caller's value and show up here.
const rcParamSeedBoundedProg = `
import "std/i32";
import "std/i64";

struct P { a: i32, b: i32, pulls: i32 }

function pull(s: P): (i32, P) {
    return (s.a, P { a: s.a + 1, b: s.b, pulls: s.pulls + 1 });
}

function thread(src: P, n: i32): i32 {
    var acc: i32 = 0;
    var s: P = src;
    var i: i32 = 0;
    while (i < n) {
        var p: (i32, P) = pull(s);
        s = p.1;
        acc = acc + p.0;
        i = i + 1;
    }
    return acc % 7;
}

// The seed binding INSIDE the loop, so the slot is re-init'd every iteration
// and the transfer inc and the re-init drop each run n times rather than once.
function loop_seed(src: P, n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var s: P = src;
        s = P { a: s.a + i, b: s.b, pulls: s.pulls };
        acc = acc + s.a;
        i = i + 1;
    }
    return acc % 7;
}

function main(): i32 {
    if (thread(P { a: 0, b: 0, pulls: 0 }, 100) < 0) { return 1; }
    var b1: i64 = __heap_bump_bytes();
    if (thread(P { a: 0, b: 0, pulls: 0 }, 200) < 0) { return 2; }
    var b2: i64 = __heap_bump_bytes();
    if (thread(P { a: 0, b: 0, pulls: 0 }, 400) < 0) { return 3; }
    var b3: i64 = __heap_bump_bytes();
    if ((b3 - b2) > (b2 - b1) * 3 / 2) { return 4; }

    var seed: P = P { a: 1, b: 2, pulls: 0 };
    if (loop_seed(seed, 100) < 0) { return 5; }
    var c1: i64 = __heap_bump_bytes();
    if (loop_seed(seed, 200) < 0) { return 6; }
    var c2: i64 = __heap_bump_bytes();
    if (loop_seed(seed, 400) < 0) { return 7; }
    var c3: i64 = __heap_bump_bytes();
    if ((c3 - c2) > (c2 - c1) * 3 / 2) { return 8; }
    if (seed.a * 10 + seed.b != 12) { return 9; }

    if (__rc_underflow_count() != 0) { return 10; }
    return 42;
}
`

func TestRcParamSeedBoundedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, rcParamSeedBoundedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (4 / 8 = the seed's borrow verdict is still governing the reassigned slot)", got)
	}
}

func TestRcParamSeedBoundedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, rcParamSeedBoundedProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (4 / 8 = the seed's borrow verdict is still governing the reassigned slot)", got)
	}
}

func TestRcParamSeedBoundedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, rcParamSeedBoundedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (4 / 8 = the seed's borrow verdict is still governing the reassigned slot)", got)
	}
}
