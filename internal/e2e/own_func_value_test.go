package e2e

import "testing"

// An `own` parameter reached through a function VALUE.
//
// A function type spells its consuming slots (`(own T) => R`), so the call
// site and the callee agree on who releases the argument. Before that they
// could not: the type said nothing, the caller reclaimed the temp it had
// built, the callee reclaimed the same box on the way out, and a fresh
// argument was freed TWICE (`apply(eat)` for `eat(own xs: i32[])` reported
// allocs=1 frees=2). The counts below are the whole point, so each program
// also returns a value that a premature free would get wrong: a balanced
// count and a right answer are two different failures.

// ownFuncValueFreshArg: a fresh construction handed to a consuming slot of a
// function value. One allocation, released once — by the callee.
const ownFuncValueFreshArg = `function eat(own xs: i32[]): i32 { return xs.len(); }
function apply(f: (own i32[]) => i32): i32 { return f([1, 2, 3]); }
function main(): i32 {
    var n: i32 = apply(eat);
    if (n != 3) { return 1; }
    return 0;
}`

// ownFuncValueFold is the accumulator convention the erased-generic fold
// rests on, at a REFERENCE instantiation: the caller hands its unit over at
// each step and takes back the one the call returns. Both visitor shapes are
// exercised — the identity (`return acc`, where the returned box IS the
// argument) and the fresh (`{ ...acc, … }`, which drops the acc it consumed)
// — through a bare NAME (which the lift wraps in a trampoline) and through a
// lambda. `held` is read back after the churn: a box freed early would be
// handed out again by the allocator and come back wrong.
const ownFuncValueFold = `struct Acc { tag: string, hits: i32 }

function keep(own a: Acc): Acc { return a; }
function bump(own a: Acc): Acc { return Acc { ...a, hits: a.hits + 1 }; }

function fold[T](own acc: T, n: i32, visit: (own T) => T): T {
    var i: i32 = 0;
    while (i < n) {
        acc = visit(acc);
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    var held: string[] = ["alpha", "beta"];
    var a: Acc = fold(Acc { tag: "a", hits: 0 }, 8, bump);
    if (a.hits != 8) { return 1; }
    if (a.tag != "a") { return 2; }
    var b: Acc = fold(Acc { tag: "b", hits: 5 }, 8, keep);
    if (b.hits != 5) { return 3; }
    if (b.tag != "b") { return 4; }
    var c: Acc = fold(Acc { tag: "c", hits: 0 }, 8, (own x: Acc) => Acc { ...x, hits: x.hits + 2 });
    if (c.hits != 16) { return 5; }
    if (held[0] != "alpha") { return 6; }
    if (held[1] != "beta") { return 7; }
    var n: i32 = fold(0, 4, (own k: i32) => k + 3);
    if (n != 12) { return 8; }
    return 0;
}`

// ownBoxedCellRebind: a local that a nested function CAPTURES and that is also
// rebound from a consuming call. closureconv boxes such a local into a
// one-element cell so the closure and the outer scope alias it, and the cell
// store released the element it superseded — but the call had already consumed
// that element, so the box was freed twice. The plain-local form of the rebind
// (`x = f(…, x, …)`) is suppressed by name; the cell read closureconv rewrote
// the name into is the same rebind and needs the same suppression.
const ownBoxedCellRebind = `function eat(own xs: string[], s: string): string[] { return xs.append(s); }
function has(xs: string[], s: string): boolean {
    for x in xs { if (x == s) { return true; } }
    return false;
}
function run(names: string[]): i32 {
    var acc: string[] = [];
    for n in names { acc = eat(acc, n); }
    function seen(s: string): boolean { return has(acc, s); }
    var hits: i32 = 0;
    for n in names { if (seen(n)) { hits = hits + 1; } }
    return hits + acc.len();
}
function main(): i32 {
    var held: string[] = ["alpha", "beta"];
    var k: i32 = 0;
    var i: i32 = 0;
    while (i < 20) { k = k + run(["a", "b", "c"]); i = i + 1; }
    if (k != 120) { return 1; }
    if (held[0] != "alpha") { return 2; }
    if (held[1] != "beta") { return 3; }
    return 0;
}`

func wantBalancedRun(t *testing.T, name, stdout, stderr string, code int) {
	t.Helper()
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want balanced at 0", name, allocs, frees, live)
	}
	if code != 0 {
		t.Errorf("%s: exit=%d (stdout %q), want 0 — a wrong answer, not just a count", name, code, stdout)
	}
}

func TestOwnFuncValueX86_64(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"fresh-arg", ownFuncValueFreshArg},
		{"fold", ownFuncValueFold},
		{"boxed-cell-rebind", ownBoxedCellRebind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runLeakCheckX86_64(t, tc.src)
			wantBalancedRun(t, tc.name, stdout, stderr, code)
		})
	}
}

func TestOwnFuncValueArm64(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"fresh-arg", ownFuncValueFreshArg},
		{"fold", ownFuncValueFold},
		{"boxed-cell-rebind", ownBoxedCellRebind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runLeakCheckArm64(t, tc.src)
			wantBalancedRun(t, tc.name, stdout, stderr, code)
		})
	}
}
