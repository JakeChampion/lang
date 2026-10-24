package e2ecompiler

import "testing"

// The two shift methods have the same short name and distinct receivers.
// wide exceeds the scalar leaf budget. Its result passes through a loop phi,
// so it needs the shared call-site proof as well as the record field rewrite.
// keep_scalar must stay a call after its record wrapper is spliced.
const inlineRecordLeafProgram = `struct Pair { a: i64, b: i64 }
function (x: i64) shift(n: i64): i64 { return x + n; }
function (p: Pair) shift(n: i64): Pair {
    return Pair { a: p.a.shift(n), b: p.b.shift(n + n) };
}
@noinline function keep_scalar(x: i64): i64 { return x; }
function (p: Pair) kept_call(): Pair {
    return Pair { a: keep_scalar(p.a), b: p.b };
}
function (p: Pair) wide(): Pair {
    let a: i64 = p.a;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    a = a * 3i64 + 1i64;
    return Pair { a: a, b: p.b + 2i64 };
}
@noinline function exercise(seed: i64): i32 {
    let before: i64 = __heap_alloc_count();
    let p: Pair = Pair { a: seed, b: seed + 1i64 };
    let expected: i64 = seed;
    let i: i32 = 0;
    while (i < 100) {
        p = p.shift(1i64).kept_call().wide();
        expected = (expected + 1i64) * 387420489i64 + 193710244i64;
        i = i + 1;
    }
    if (p.a != expected || p.b != seed + 401i64) { return 1; }
    if (__heap_alloc_count() != before) { return 2; }
    return 0;
}
@noinline function kept_result(seed: i64): i32 {
    let p: Pair = Pair { a: seed, b: seed + 1i64 };
    let first: Pair[] = [p.wide()];
    let second: Pair[] = first.append(p.shift(2i64).wide());
    if (first.len() != 1 || second.len() != 2) { return 3; }
    if (first[0].a != seed * 387420489i64 + 193710244i64 || first[0].b != seed + 3i64) { return 4; }
    if (second[1].b != seed + 7i64 || p.a != seed || p.b != seed + 1i64) { return 5; }
    return 0;
}
function main(): i32 {
    let result: i32 = exercise(args().len() as i64);
    if (result != 0) { return result; }
    let kept: i32 = kept_result(args().len() as i64);
    if (kept != 0) { return kept; }
    print("ok");
    return 0;
}
`

func TestSelfHostInlineScalarRecordLeaves(t *testing.T) {
	runSemanticProgram(t, "record-leaves", inlineRecordLeafProgram,
		[]string{"exercise", "kept_result", "i64.shift", "Pair.shift", "Pair.wide", "Pair.kept_call", "keep_scalar"},
		semInlineWants("ok\n"), "keep_scalar")
}
