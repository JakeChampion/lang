package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A fresh argument temp is released after a call whose result is a pointer,
// when the callee holds the argument only through counted stores or not at
// all. The callee is named, a function-typed local or a struct field, and the
// temp is an inline `.with` copy, an array literal or a struct literal. Each
// program's result is the interpreter's.
func TestPointerResultCallReleasesItsArgumentTemps(t *testing.T) {
	cases := []struct {
		name string
		want int
		src  string
	}{
		{"with-argument-to-a-recursive-builder", 1, `struct RThread { pc: i32, caps: i32[] }
struct RAdd { seen: i32[], gen: i32, threads: RThread[] }
enum RInst { IChar(i32), IClass(string), ISave(i32), IMatch }
function add(st: RAdd, prog: RInst[], pc: i32, caps: i32[], ti: i32): RAdd {
    var sn: i32[] = st.seen;
    var g: i32 = st.gen;
    var ths: RThread[] = st.threads;
    if (sn[pc] == g) { return RAdd { seen: sn, gen: g, threads: ths }; }
    sn = sn.with(pc, g);
    match (prog[pc]) {
        ISave(slot) => { return add(RAdd { seen: sn, gen: g, threads: ths }, prog, pc + 1, caps.with(slot, ti), ti); },
        _ => { return RAdd { seen: sn, gen: g, threads: ths.append(RThread { pc: pc, caps: caps }) }; }
    }
}
function main(): i32 {
    var prog: RInst[] = [ISave(0), IChar(97), ISave(1), IMatch];
    var init: i32[] = [0 - 1, 0 - 1];
    var seen: i32[] = [0, 0, 0, 0];
    var st: RAdd = add(RAdd { seen: seen, gen: 1, threads: [] }, prog, 0, init, 0);
    return st.threads.len();
}`},
		{"with-argument-held-by-the-result", 3, `struct H { xs: i32[] }
function hold(xs: i32[]): H { return H { xs: xs }; }
function main(): i32 {
    var caps: i32[] = [1, 2, 3];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { var h: H = hold(caps.with(2, i)); acc = acc + h.xs[2]; i = i + 1; }
    return acc;
}`},
		{"literal-argument-to-a-with-result", 42, `function set0(xs: i32[]): i32[] { return xs.with(0, 9); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { var s: i32[] = set0([4, 5, 6]); acc = acc + s[0] + s[1]; i = i + 1; }
    return acc;
}`},
		{"struct-argument-through-a-local", 4, `struct C { value: i32 }
function bump(c: C): Result[C, string] { return Ok(C { value: c.value + 1 }); }
function main(): i32 {
    var f: (C) => Result[C, string] = bump;
    match (f(C { value: 3 })) {
        Ok(n) => { return n.value; },
        Err(e) => { return 9; }
    }
    return 8;
}`},
		{"struct-argument-through-a-field", 10, `struct C { value: i32 }
struct R { name: string, run: (C) => Result[C, string] }
function bump(c: C): Result[C, string] { return Ok(C { value: c.value + 1 }); }
function main(): i32 {
    var r: R = R { name: "r", run: bump };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (r.run(C { value: i })) { Ok(n) => { acc = acc + n.value; }, Err(e) => { acc = acc + 100; } }
        i = i + 1;
    }
    return acc;
}`},
		{"argument-stored-in-the-result-by-a-function-value", 42, `struct W { c: C, n: i32 }
struct C { value: i32 }
function wrap(c: C): W { return W { c: c, n: 1 }; }
function main(): i32 {
    var g: (C) => W = wrap;
    var h: (C) => W = (c: C) => W { c: c, n: 2 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var w: W = g(C { value: i + 10 });
        var v: W = h(C { value: i });
        acc = acc + w.c.value + v.c.value + v.n;
        i = i + 1;
    }
    return acc;
}`},
		{"struct-argument-to-an-identity-through-a-local", 36, `struct C { value: i32 }
struct W { c: C, n: i32 }
function id(c: C): C { return c; }
function wrap(c: C): W { return W { c: c, n: 1 }; }
function main(): i32 {
    var f: (C) => C = id;
    var g: (C) => W = wrap;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var r: C = f(C { value: i });
        var w: W = g(C { value: i + 10 });
        acc = acc + r.value + w.c.value;
        i = i + 1;
    }
    return acc;
}`},
	}
	for _, c := range cases {
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, c.src, c.want, runSanitizeX86_64)
		})
		t.Run("arm64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, c.src, c.want, runSanitizeArm64)
		})
		t.Run("wasm-leakcheck/"+c.name, func(t *testing.T) {
			stdout, stderr, _ := runLeakCheckWasm(t, c.src, true)
			if got := strings.TrimSpace(stdout); got != strconv.Itoa(c.want) {
				t.Fatalf("result=%q, want %d\n%s", got, c.want, stderr)
			}
			if strings.Contains(stderr, "fern-sanitizer:") {
				t.Errorf("sanitizer finding: %q", stderr)
			}
			allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
			if allocs != frees || live != 0 {
				t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
			}
		})
	}
}

// An array handed back bare is a retention the summary refuses, so a
// function value that might be `keep` keeps the temps passed through it. The
// result must still read what the argument held.
func TestPointerResultCallKeepsATempAnIdentityCalleeReturns(t *testing.T) {
	src := `function keep(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    var k: (i32[]) => i32[] = keep;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: i32[] = k([i, i + 1, i + 2]);
        acc = acc + a[0] + a[2];
        i = i + 1;
    }
    return acc;
}`
	for _, run := range []struct {
		name string
		fn   func(*testing.T, string) (string, string, int)
	}{{"x86_64", runSanitizeX86_64}, {"arm64", runSanitizeArm64}} {
		t.Run(run.name, func(t *testing.T) {
			stdout, stderr, code := run.fn(t, src)
			if code != 12 {
				t.Fatalf("exit = %d, want 12\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.Contains(line, "fern-sanitizer:") && !strings.Contains(line, "fern-sanitizer: leak") {
					t.Errorf("sanitizer finding: %s", line)
				}
			}
		})
	}
}
