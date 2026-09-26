package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Two struct-local shapes the AST lowering (FERN_SEM_IR=) left without an exit
// release, where native and the semantic lowering are clean:
//
//   - #10171: an alias declared once and reassigned from its source in a loop
//     (`var prev = s; while … { prev = s; s = S { ...s, … } }`). Each `prev = s`
//     retains, so prev holds a counted share of s at every point, and both
//     locals now keep their credit.
//   - #10162: a local bound from a borrowed parameter and rebound to a box the
//     frame builds (`var a = p; a = St { ...a, … }`). Its exit release is its
//     rebind release with p's box as the new value, so it frees only a box that
//     is not p's.
//
// Every program answers 99 if a release ran past zero; the `hostile` rows are
// shapes the credit must not over-release, checked for the answer and the
// sanitizer. A hostile row the credit reaches (a zero `refused`) must balance;
// one it refuses pins the AST leg's alloc and free counts, so more frees there
// means a widening reached a shape this credit refuses.
var structAliasShareCases = []struct {
	name    string
	src     string
	want    int
	refused [2]int64 // allocs, frees on the AST leg; zero for a balanced row
}{
	{"prev_reassigned_in_loop", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    var s: S = S { ops: [1, 2, 3], n: 0 };
    var prev: S = s;
    var i: i32 = 0;
    while (i < x) {
        prev = s;
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n + prev.n + prev.ops[0];
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 100) { t = t + run(j % 3); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`, 40, [2]int64{}},
	{"hostile_prev_from_other", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    var s: S = S { ops: [1, 2, 3], n: 0 };
    var o: S = S { ops: [7, 8], n: 5 };
    var prev: S = s;
    var i: i32 = 0;
    while (i < x) {
        prev = s;
        if (i == 1) { prev = o; }
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n + prev.n + prev.ops[0] + o.ops[1];
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 100) { t = t + run(j % 3); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`, 0, [2]int64{598, 0}},
	{"param_alias_rebound", `struct St { ops: i32[], n: i32 }
function take(p: St): i32 {
    var a: St = p;
    a = St { ...a, ops: a.ops.append(1) };
    return a.ops.len() + p.ops.len();
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 50) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s);
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 50, [2]int64{}},
	{"hostile_conditional_rebind", `struct St { ops: i32[], n: i32 }
function take(p: St, k: i32): i32 {
    var a: St = p;
    if (k % 2 == 0) { a = St { ...a, ops: a.ops.append(k) }; }
    return a.ops.len() + p.ops[0];
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 50) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s, j) + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0, [2]int64{}},
	{"hostile_handback_rebind", `struct St { ops: i32[], n: i32 }
function same(x: St): St { return x; }
function take(p: St): i32 {
    var a: St = p;
    a = same(a);
    a = St { ...a, n: a.n + 1 };
    a = same(a);
    return a.n + a.ops.len() + p.ops.len();
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 50) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s) + s.ops[0];
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0, [2]int64{150, 100}},
	{"hostile_returned", `struct St { ops: i32[], n: i32 }
function grow(p: St): St {
    var a: St = p;
    a = St { ...a, ops: a.ops.append(9) };
    return a;
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 50) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        var g: St = grow(s);
        t = t + g.ops.len() + g.ops[4] + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0, [2]int64{250, 150}},
	{"hostile_returned_in_tuple", `struct St { ops: i32[], n: i32 }
function grow(p: St, k: i32): (i32, St) {
    var a: St = p;
    a = St { ...a, ops: a.ops.append(k) };
    return (k, a);
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 50) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        var g: (i32, St) = grow(s, j);
        var junk: St = St { ops: [9, 9, 9, 9, 9], n: 9 };
        t = t + g.1.ops.len() + g.1.ops[4] + g.0 + junk.n;
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0, [2]int64{400, 300}},
	{"hostile_field_returned", `struct St { ops: i32[], n: i32 }
function grown_ops(p: St, k: i32): i32[] {
    var a: St = p;
    a = St { ...a, ops: a.ops.append(k) };
    return a.ops;
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 50) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        var o: i32[] = grown_ops(s, j);
        var junk: i32[] = [9, 9, 9, 9, 9];
        t = t + o.len() + o[4] + junk[0];
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0, [2]int64{300, 200}},
	{"hostile_early_return", `struct St { ops: i32[], n: i32 }
function take(p: St, k: i32): i32 {
    var a: St = p;
    a = St { ...a, ops: a.ops.append(k) };
    if (k % 3 == 0) { return a.ops.len(); }
    a = St { ...a, n: 7 };
    return a.n + p.ops.len();
}
function main(): i32 {
    var t: i32 = 0; var j: i32 = 0;
    while (j < 60) {
        var s: St = St { ops: [j, 1, 2, 3], n: 0 };
        t = t + take(s, j) + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}
`, 0, [2]int64{}},
}

func TestSelfHostStructAliasShareReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range structAliasShareCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			want := tc.want
			for _, sem := range []string{"FERN_SEM_IR=1", "FERN_SEM_IR="} {
				for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
					stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode, sem), nil)
					if want == 0 && sem == "FERN_SEM_IR=1" {
						want = exit
					}
					balanced := tc.refused == [2]int64{}
					if exit == 99 || exit != want || structAliasSanitizerFault(stderr, balanced) {
						t.Fatalf("%s %s: exit = %d, want %d (99 = rc underflow), and no sanitizer fault\n%s", sem, mode, exit, want, stderr)
					}
					if mode == "FERN_LEAKCHECK=1" && (balanced || sem == "FERN_SEM_IR=1") {
						assertBalancedCensus(t, stderr)
					} else if mode == "FERN_LEAKCHECK=1" {
						assertRefusedCensus(t, stderr, tc.refused)
					}
				}
			}
		})
	}
}

func TestSelfHostStructAliasShareReleaseWasm(t *testing.T) {
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
			want := tc.want
			for _, sem := range []string{"FERN_SEM_IR=1", "FERN_SEM_IR="} {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", sem))
				if want == 0 && sem == "FERN_SEM_IR=1" {
					want = exit
				}
				if exit == 99 || exit != want {
					t.Fatalf("%s: exit = %d, want %d (99 = rc underflow)\n%s", sem, exit, want, stderr)
				}
				if tc.refused == [2]int64{} || sem == "FERN_SEM_IR=1" {
					assertBalancedCensus(t, stderr)
				} else {
					assertRefusedCensus(t, stderr, tc.refused)
				}
			}
		})
	}
}

// structAliasSanitizerFault: a sanitizer report other than a leak, or any
// report at all when the row must balance.
func structAliasSanitizerFault(stderr string, balanced bool) bool {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "fern-sanitizer:") && (balanced || !strings.HasPrefix(line, "fern-sanitizer: leak ")) {
			return true
		}
	}
	return false
}
