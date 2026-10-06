package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Two struct-local shapes that need an exit release:
//
//   - #10171: an alias declared once and reassigned from its source in a loop
//     (`let prev = s; while … { prev = s; s = S { ...s, … } }`). Each `prev = s`
//     retains, so prev holds a counted share of s at every point, and both
//     locals now keep their credit.
//   - #10162: a local bound from a borrowed parameter and rebound to a box the
//     frame builds (`let a = p; a = St { ...a, … }`). Its exit release is its
//     rebind release with p's box as the new value, so it frees only a box that
//     is not p's.
//
// Every program answers 99 if a release ran past zero; the `hostile` rows are
// shapes the credit must not over-release. Every row balances; a zero `want`
// takes the first run's answer and holds the others to it.
var structAliasShareCases = []struct {
	name string
	src  string
	want int
}{
	{"prev_reassigned_in_loop", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    let s: S = S { ops: [1, 2, 3], n: 0 };
    let prev: S = s;
    let i: i32 = 0;
    while (i < x) {
        prev = s;
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n + prev.n + prev.ops[0];
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 100) { t = t + run(j % 3); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`, 40},
	{"hostile_prev_from_other", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    let s: S = S { ops: [1, 2, 3], n: 0 };
    let o: S = S { ops: [7, 8], n: 5 };
    let prev: S = s;
    let i: i32 = 0;
    while (i < x) {
        prev = s;
        if (i == 1) { prev = o; }
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n + prev.n + prev.ops[0] + o.ops[1];
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 100) { t = t + run(j % 3); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`, 0},
	{"param_alias_rebound", `struct St { ops: i32[], n: i32 }
function take(p: St): i32 {
    let a: St = p;
    a = St { ...a, ops: a.ops.append(1) };
    return a.ops.len() + p.ops.len();
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s);
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 50},
	{"hostile_conditional_rebind", `struct St { ops: i32[], n: i32 }
function take(p: St, k: i32): i32 {
    let a: St = p;
    if (k % 2 == 0) { a = St { ...a, ops: a.ops.append(k) }; }
    return a.ops.len() + p.ops[0];
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s, j) + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0},
	{"hostile_handback_rebind", `struct St { ops: i32[], n: i32 }
function same(x: St): St { return x; }
function take(p: St): i32 {
    let a: St = p;
    a = same(a);
    a = St { ...a, n: a.n + 1 };
    a = same(a);
    return a.n + a.ops.len() + p.ops.len();
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s) + s.ops[0];
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0},
	{"hostile_returned", `struct St { ops: i32[], n: i32 }
function grow(p: St): St {
    let a: St = p;
    a = St { ...a, ops: a.ops.append(9) };
    return a;
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        let g: St = grow(s);
        t = t + g.ops.len() + g.ops[4] + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0},
	{"hostile_returned_in_tuple", `struct St { ops: i32[], n: i32 }
function grow(p: St, k: i32): (i32, St) {
    let a: St = p;
    a = St { ...a, ops: a.ops.append(k) };
    return (k, a);
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        let g: (i32, St) = grow(s, j);
        let junk: St = St { ops: [9, 9, 9, 9, 9], n: 9 };
        t = t + g.1.ops.len() + g.1.ops[4] + g.0 + junk.n;
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0},
	{"hostile_field_returned", `struct St { ops: i32[], n: i32 }
function grown_ops(p: St, k: i32): i32[] {
    let a: St = p;
    a = St { ...a, ops: a.ops.append(k) };
    return a.ops;
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        let o: i32[] = grown_ops(s, j);
        let junk: i32[] = [9, 9, 9, 9, 9];
        t = t + o.len() + o[4] + junk[0];
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0},
	{"hostile_early_return", `struct St { ops: i32[], n: i32 }
function take(p: St, k: i32): i32 {
    let a: St = p;
    a = St { ...a, ops: a.ops.append(k) };
    if (k % 3 == 0) { return a.ops.len(); }
    a = St { ...a, n: 7 };
    return a.n + p.ops.len();
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 60) {
        let s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s, j) + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0},
}

func TestSelfHostStructAliasShareReleaseX86_64(t *testing.T) {
	boxedProbes(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range structAliasShareCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			want := tc.want
			for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
				if want == 0 {
					want = exit
				}
				if exit == 99 || exit != want || forArrStructSanitizerFault(stderr, true) {
					t.Fatalf("%s: exit = %d, want %d (99 = rc underflow), and no sanitizer fault\n%s", mode, exit, want, stderr)
				}
				if mode == "FERN_LEAKCHECK=1" {
					assertBalancedCensus(t, stderr)
				}
			}
		})
	}
}

func TestSelfHostStructAliasShareReleaseWasm(t *testing.T) {
	boxedProbes(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range structAliasShareCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit == 99 || (tc.want != 0 && exit != tc.want) {
				t.Fatalf("exit = %d, want %d (99 = rc underflow)\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
