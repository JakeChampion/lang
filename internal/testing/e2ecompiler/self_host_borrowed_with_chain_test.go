package e2ecompiler

import "testing"

// A `.with` chain through a borrowed array parameter writes in place when the
// caller is done with the array, and copies when a caller still holds it
// (#12332). The receiver's retain is deferred as an append's is, so the count
// test sees the caller's units rather than the frame's own: a unique update
// chain allocates nothing, and a retained alias or a read of the parameter
// after the update still sees the value it had.

const borrowedWithHelpers = `function swap(xs: i32[]): i32[] {
    let first: i32 = xs[0];
    let last: i32 = xs[1];
    xs = xs.with(0, last);
    xs = xs.with(1, first);
    return xs;
}
function swap_names(xs: string[]): string[] {
    let first: string = xs[0];
    let last: string = xs[1];
    xs = xs.with(0, last);
    xs = xs.with(1, first);
    return xs;
}
function bump_all(xs: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < xs.len()) { xs = xs.with(i, xs[i] + n); i = i + 1; }
    return xs;
}
function twice(xs: i32[]): i32[] {
    return swap(swap(xs));
}
function pair(a: i32, b: i32): i32[] {
    let xs: i32[] = [];
    xs = xs.append(a);
    return xs.append(b);
}
function names(a: string, b: string): string[] {
    let xs: string[] = [];
    xs = xs.append(a + "");
    return xs.append(b + "");
}
`

// Exits 42 when the unique chains allocate nothing, the shared ones copy
// once, and every value read back is the one written.
const borrowedWithAllocsSrc = borrowedWithHelpers + `function main(): i32 {
    let a: i32[] = pair(1, 2);
    let c0: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 100) { a = swap(a); i = i + 1; }
    if (__heap_alloc_count() - c0 != 0) { return 1; }
    let old: i32[] = a;
    let c1: i64 = __heap_alloc_count();
    let b: i32[] = swap(a);
    if (__heap_alloc_count() - c1 != 1) { return 2; }
    if (old[0] != 1 || old[1] != 2 || b[0] != 2 || b[1] != 1) { return 3; }
    let s: string[] = names("x", "y");
    let c2: i64 = __heap_alloc_count();
    i = 0;
    while (i < 100) { s = swap_names(s); i = i + 1; }
    if (__heap_alloc_count() - c2 != 0) { return 4; }
    if (s[0] != "x" || s[1] != "y") { return 5; }
    let v: i32[] = pair(0, 0);
    let c3: i64 = __heap_alloc_count();
    i = 0;
    while (i < 50) { v = bump_all(v, 1); i = i + 1; }
    if (__heap_alloc_count() - c3 != 0) { return 6; }
    if (v[0] != 50 || v[1] != 50) { return 7; }
    let c4: i64 = __heap_alloc_count();
    i = 0;
    while (i < 50) { v = twice(v); i = i + 1; }
    if (__heap_alloc_count() - c4 != 0) { return 8; }
    return 42;
}
`

func TestSelfHostBorrowedWithChainAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, borrowedWithAllocsSrc, target); code != 42 {
				t.Errorf("exited %d, want 42 (1, 4, 6 and 8 are a copy per unique update; 2 a shared one written in place)\n%s", code, stderr)
			}
		})
	}
}

// The same updates under the leak census and the sanitizer, with the callers
// that must still see the old value: a retained alias, a wrapper handing the
// array on, and a callee reading its parameter after updating it. Answers are
// the interpreter's, and stay below 126 for wasmtime's exit status.
var borrowedWithChainRows = []leakRow{
	{"scalar_chain", borrowedWithHelpers + `function main(): i32 {
    let a: i32[] = pair(1, 2);
    let i: i32 = 0;
    while (i < 7) { a = swap(a); i = i + 1; }
    return a[0] * 10 + a[1];
}
`, true},
	{"shared_alias", borrowedWithHelpers + `function main(): i32 {
    let a: i32[] = pair(1, 2);
    let old: i32[] = a;
    a = swap(a);
    let kept: i32[] = a;
    a = twice(a);
    return old[0] * 27 + old[1] * 9 + kept[0] * 3 + a[1];
}
`, true},
	{"string_chain", borrowedWithHelpers + `function main(): i32 {
    let s: string[] = names("ab", "c");
    let old: string[] = s;
    let i: i32 = 0;
    while (i < 5) { s = swap_names(s); i = i + 1; }
    return s[0].len() * 10 + old[0].len();
}
`, true},
	{"loop_chain", borrowedWithHelpers + `function main(): i32 {
    let v: i32[] = pair(3, 4);
    let old: i32[] = v;
    let i: i32 = 0;
    while (i < 9) { v = bump_all(v, 2); i = i + 1; }
    return v[0] + v[1] + old[0];
}
`, true},
	{"function_value", borrowedWithHelpers + `function apply(f: (i32[]) => i32[], xs: i32[]): i32[] {
    return f(xs);
}
function main(): i32 {
    let a: i32[] = pair(1, 2);
    let old: i32[] = a;
    let f: (i32[]) => i32[] = swap;
    let b: i32[] = f(a);
    let c: i32[] = apply(swap, a);
    return old[0] * 27 + old[1] * 9 + b[0] * 3 + c[1];
}
`, true},
	{"read_after_update", `function peek(xs: i32[]): i32 {
    let ys: i32[] = xs.with(0, 9);
    return xs[0] * 10 + ys[0];
}
function main(): i32 {
    let a: i32[] = [];
    a = a.append(4);
    let r: i32 = peek(a);
    return r + a[0];
}
`, true},
	{"nested_arrays", `function swap_rows(xs: i32[][]): i32[][] {
    let first: i32[] = xs[0];
    let last: i32[] = xs[1];
    xs = xs.with(0, last);
    xs = xs.with(1, first);
    return xs;
}
function main(): i32 {
    let row: i32[] = [];
    row = row.append(5);
    let other: i32[] = [];
    other = other.append(6);
    let grid: i32[][] = [];
    grid = grid.append(row);
    grid = grid.append(other);
    let old: i32[][] = grid;
    let i: i32 = 0;
    while (i < 3) { grid = swap_rows(grid); i = i + 1; }
    return grid[0][0] * 10 + old[0][0];
}
`, true},
}

func TestSelfHostBorrowedWithChainX86_64(t *testing.T) {
	runLeakRowsX86_64(t, borrowedWithChainRows)
}

func TestSelfHostBorrowedWithChainArm64(t *testing.T) {
	runLeakRowsArm64(t, borrowedWithChainRows)
}

func TestSelfHostBorrowedWithChainWasm(t *testing.T) {
	runLeakRowsWasm(t, borrowedWithChainRows)
}
