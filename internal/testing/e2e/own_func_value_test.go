package e2e

import "testing"

const ownFuncValueFreshArg = `function eat(own xs: i32[]): i32 { return xs.len(); }
function apply(f: (own i32[]) => i32): i32 { return f([1, 2, 3]); }
function main(): i32 {
    let n: i32 = apply(eat);
    if (n != 3) { return 1; }
    return 0;
}`

const ownFuncValueFold = `struct Acc { tag: string, hits: i32 }

@noinline function keep(own a: Acc): Acc { return a; }
function bump(own a: Acc): Acc { return Acc { ...a, hits: a.hits + 1 }; }

function fold[T](own acc: T, n: i32, visit: (own T) => T): T {
    let i: i32 = 0;
    while (i < n) {
        acc = visit(acc);
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    let held: string[] = ["alpha", "beta"];
    let a: Acc = fold(Acc { tag: "a", hits: 0 }, 8, bump);
    if (a.hits != 8) { return 1; }
    if (a.tag != "a") { return 2; }
    let b: Acc = fold(Acc { tag: "b", hits: 5 }, 8, keep);
    if (b.hits != 5) { return 3; }
    if (b.tag != "b") { return 4; }
    let c: Acc = fold(Acc { tag: "c", hits: 0 }, 8, (own x: Acc) => Acc { ...x, hits: x.hits + 2 });
    if (c.hits != 16) { return 5; }
    if (held[0] != "alpha") { return 6; }
    if (held[1] != "beta") { return 7; }
    let n: i32 = fold(0, 4, (own k: i32) => k + 3);
    if (n != 12) { return 8; }
    return 0;
}`

const ownBoxedCellRebind = `function eat(own xs: string[], s: string): string[] { return xs.append(s); }
function has(xs: string[], s: string): boolean {
    for x in xs { if (x == s) { return true; } }
    return false;
}
function run(names: string[]): i32 {
    let acc: string[] = [];
    for n in names { acc = eat(acc, n); }
    function seen(s: string): boolean { return has(acc, s); }
    let hits: i32 = 0;
    for n in names { if (seen(n)) { hits = hits + 1; } }
    return hits + acc.len();
}
function main(): i32 {
    let held: string[] = ["alpha", "beta"];
    let k: i32 = 0;
    let i: i32 = 0;
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
