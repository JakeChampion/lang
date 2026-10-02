package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// appendResultTempCases pin #10560: an expression-position `.append` result
// used as another append's receiver (`acc.append(a).append(b)`) or passed as a
// call argument (`id(xs.append(1))`, `rec(n - 1, acc.append(x))`) must not
// strand its buffer's count. Each row must balance under the leakcheck census
// and run clean under FERN_SANITIZE=1 on x86-64, and balance on wasm.
var appendResultTempCases = []struct {
	name string
	src  string
	want int
}{
	// The issue's receiver repro: a chain on a borrowed parameter, and a chain
	// rebinding a local. The outer push copies or grows the inner result, whose
	// count nothing else holds. 122 allocs / 67 frees before.
	{"chain_receiver", `function step(n: i32, acc: i32[]): i32[] {
    return acc.append(n).append(n + 1);
}
function main(): i32 {
    var pending: i32[] = [];
    var fd: i32 = 0;
    while (fd < 60) { pending = step(fd, pending); fd = fd + 1; }
    var q: i32[] = [];
    fd = 0;
    while (fd < 60) { q = q.append(fd).append(fd); fd = fd + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (pending.len() + q.len()) % 100;
}
`, 40},
	// The argument repro: a callee that hands its parameter back retained. 11
	// allocs / 1 free before.
	{"arg_identity_callee", `function id(acc: i32[]): i32[] { return acc; }
function main(): i32 {
    var pending: i32[] = [];
    var fd: i32 = 0;
    while (fd < 10) { pending = id(pending.append(fd)); fd = fd + 1; }
    var k: i32[] = id(id(pending.append(1)).append(2));
    if (k.len() != 12 || pending.len() != 10) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return pending.len();
}
`, 10},
	// A recursive accumulator: every frame's append result is an argument of
	// the next. 6 allocs / 0 frees before.
	{"arg_recursive_accumulator", `function rec(n: i32, acc: i32[]): i32[] {
    if (n == 0) { return acc; }
    return rec(n - 1, acc.append(n));
}
function main(): i32 {
    var r: i32[] = rec(10, []);
    if (r[0] != 10 || r[9] != 1) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return r.len();
}
`, 10},
	// The 8-byte pushes. `f(xs)` returning `p.append(v)` in place handed the
	// caller's buffer back uncounted, which the rebind then freed under `xs`.
	{"wide_elements", `function f(p: f64[]): f64[] { return p.append(1.5); }
function stepf(n: i32, acc: f64[]): f64[] { return acc.append(n as f64).append(0.5); }
function stepi(n: i32, acc: i64[]): i64[] { return acc.append(n as i64).append(7 as i64); }
function idf(acc: f64[]): f64[] { return acc; }
function main(): i32 {
    var xs: f64[] = [];
    var fs: f64[] = [];
    var is: i64[] = [];
    var g: f64[] = [];
    var i: i32 = 0;
    while (i < 30) {
        xs = f(xs);
        fs = stepf(i, fs);
        is = stepi(i, is);
        g = g.append(1.0).append(2.0);
        g = idf(g.append(3.0));
        i = i + 1;
    }
    if (xs[29] != 1.5 || is[59] != (7 as i64) || g.len() != 90) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (xs.len() + fs.len() + is.len()) % 100;
}
`, 50},
	{"string_elements_chain", `import "std/i32";
function tag(k: i32): string { return "t" + k.to_string(); }
function step(n: i32, acc: string[]): string[] {
    return acc.append(tag(n)).append(tag(n + 1));
}
function main(): i32 {
    var pending: string[] = [];
    var q: string[] = [];
    var i: i32 = 0;
    while (i < 30) {
        pending = step(i, pending);
        q = q.append(tag(i)).append(tag(i + 100));
        i = i + 1;
    }
    if (pending[59] != "t30" || q[59] != "t129") { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return pending.len() + q.len();
}
`, 120},
	{"struct_elements_chain", `struct P { v: i32, w: i32 }
function step(n: i32, acc: P[]): P[] {
    return acc.append(P { v: n, w: 1 }).append(P { v: n + 1, w: 2 });
}
function main(): i32 {
    var pending: P[] = [];
    var q: P[] = [];
    var i: i32 = 0;
    while (i < 30) {
        pending = step(i, pending);
        q = q.append(P { v: i, w: 3 }).append(P { v: i, w: 4 });
        i = i + 1;
    }
    var total: i32 = 0;
    for p in pending { total = total + p.v + p.w; }
    for p2 in q { total = total + p2.w; }
    if (__rc_underflow_count() != 0) { return 99; }
    return total % 100;
}
`, 0},
	// The in-place guard. `grow` and `rec` push onto a buffer their caller no
	// longer reads, and `roomy.append(20)` must copy because `roomy` is read
	// again. The allocation count is the one main had, so a release that made a
	// push see a shared buffer, or an in-place push that stopped being one,
	// moves it.
	{"inplace_guard", `function grow(acc: i32[], n: i32): i32[] { return acc.append(n); }
function rec(n: i32, acc: i32[]): i32[] {
    if (n == 0) { return acc; }
    return rec(n - 1, acc.append(n));
}
function sum(xs: i32[]): i32 { var t: i32 = 0; for x in xs { t = t + x; } return t; }
function main(): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < 1000) { xs = grow(xs, i); i = i + 1; }
    var r: i32[] = rec(1000, []);
    var roomy: i32[] = [];
    i = 0;
    while (i < 3) { roomy = roomy.append(i); i = i + 1; }
    var a: i32 = sum(roomy.append(20));
    var b: i32 = sum(roomy);
    if (a != b + 20 || roomy.len() != 3) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (xs.len() + r.len()) % 100;
}
`, 0},
	// Callees that KEEP the argument: in a returned struct, in a returned
	// array literal, and as an element appended to another array. The first
	// retains it at the construction, so its temp is released; the other two
	// are not proven counted and keep their temps. Nothing may be released
	// under a holder, which the sanitizer and the values read back check.
	{"callee_keeps_argument", `struct Holder { xs: i32[], n: i32 }
function hold(xs: i32[]): Holder { return Holder { xs: xs, n: xs.len() }; }
function wrap(xs: i32[]): i32[][] { return [xs]; }
function push_into(out: i32[][], xs: i32[]): i32[][] { return out.append(xs); }
function main(): i32 {
    var ys: i32[] = [1, 2];
    var outs: i32[][] = [];
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var h: Holder = hold(ys.append(i));
        var w: i32[][] = wrap(ys.append(i));
        outs = push_into(outs, ys.append(i).append(i));
        total = total + h.xs[2] + h.n + w[0][2];
        i = i + 1;
    }
    for o in outs { total = total + o[3]; }
    if (ys.len() != 2 || outs.len() != 20) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return total % 100;
}
`, 30},
}

func writeAppendTempSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostAppendResultTempX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range appendResultTempCases {
		src := writeAppendTempSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer report\n%s", exit, tc.want, stderr)
			}
		})
	}
}

func TestSelfHostAppendResultTempWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range appendResultTempCases {
		src := writeAppendTempSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
