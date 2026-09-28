package e2eselfhost

import (
	"testing"
)

// An rc enum returned by a call through a function value carries one count
// the caller releases (#10577). Every closure body of an enum result is an
// "ENUM:" member that retains a return it holds no count for, so a call
// through a function value of that result ("CLOENUM:") is bound, lent and
// rebound like a member call; where every such body returns a fresh chain
// ("CLORCE:") it is an "RCE:" call. The programs cover a string, an array and
// a closure payload, each bound, rebound in a loop and lent, through a lambda,
// a named function's trampoline and a capturing lambda. Each runs ten rounds
// and returns 99 if __rc_underflow_count() moved.

// The issue's probe.
const closureCallEnumIssueSrc = `enum S { Done(i32), Next(i32, string) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    return Next(n, "ab" + "c");
}
function run(s: S, g: (i32) => S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return t.len() + run(g(w), g); }
    }
    return -1;
}
function main(): i32 {
    var g: (i32) => S = (x: i32): S => mk(x - 1);
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { total = total + run(mk(4), g); i = i + 1; }
    return total;
}
`

const closureCallEnumStringSrc = `enum S { Done(i32), Next(i32, string) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    return Next(n, "ab" + "c");
}
function run(s: S, g: (i32) => S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return t.len() + run(g(w), g); }
    }
    return -1;
}
function walk(n: i32, g: (i32) => S): i32 {
    var cur: S = g(n);
    var acc: i32 = 0;
    var guard: i32 = 0;
    while (guard < 100) {
        match (cur) {
            Done(v) => { return acc + v; },
            Next(w, t) => { acc = acc + t.len(); cur = g(w); }
        }
        guard = guard + 1;
    }
    return -1;
}
function first(g: (i32) => S, n: i32): i32 {
    var e: S = g(n);
    match (e) {
        Done(v) => { return v; },
        Next(w, t) => { return w + t.len(); }
    }
    return -1;
}
function round(i: i32): i32 {
    var k: i32 = i % 3;
    var g: (i32) => S = (x: i32): S => mk(x - 1);
    var h: (i32) => S = mk;
    var c: (i32) => S = (x: i32): S => mk(x - 1 - k);
    return run(mk(4), g) + walk(4, g) + walk(3, c) + first(h, 2) + first(c, i) + run(g(3), c);
}
` + closureCallEnumMain

const closureCallEnumArraySrc = `enum S { Done(i32), Next(i32, i32[]) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    return Next(n, [n, n + 1, 7]);
}
function run(s: S, g: (i32) => S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return t[2] + run(g(w), g); }
    }
    return -1;
}
function walk(n: i32, g: (i32) => S): i32 {
    var cur: S = g(n);
    var acc: i32 = 0;
    var guard: i32 = 0;
    while (guard < 100) {
        match (cur) {
            Done(v) => { return acc + v; },
            Next(w, t) => { acc = acc + t[2]; cur = g(w); }
        }
        guard = guard + 1;
    }
    return -1;
}
function first(g: (i32) => S, n: i32): i32 {
    var e: S = g(n);
    match (e) {
        Done(v) => { return v; },
        Next(w, t) => { return w + t[2]; }
    }
    return -1;
}
function round(i: i32): i32 {
    var k: i32 = i % 3;
    var g: (i32) => S = (x: i32): S => mk(x - 1);
    var h: (i32) => S = mk;
    var c: (i32) => S = (x: i32): S => mk(x - 1 - k);
    return run(mk(4), g) + walk(4, g) + walk(3, c) + first(h, 2) + first(c, i) + run(g(3), c);
}
` + closureCallEnumMain

// #9841's chain, walked by recursion through the payload's closure.
const closureCallEnumClosureSrc = `enum Step { Done(i32), Next(i32, (i32) => Step) }
function make(n: i32, k: i32): Step {
    if (n <= 0) { return Done(k); }
    return Next(n, (x: i32): Step => make(n - 1, k + x));
}
function run(s: Step): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, f) => { return run(f(w)); }
    }
    return -1;
}
function walk(n: i32, g: (i32) => Step): i32 {
    var cur: Step = g(n);
    var acc: i32 = 0;
    var guard: i32 = 0;
    while (guard < 100) {
        match (cur) {
            Done(v) => { return acc + v; },
            Next(w, f) => { acc = acc + w; cur = g(w - 1); }
        }
        guard = guard + 1;
    }
    return -1;
}
function first(g: (i32) => Step, n: i32): i32 {
    var e: Step = g(n);
    match (e) {
        Done(v) => { return v; },
        Next(w, f) => { return w + run(f(1)); }
    }
    return -1;
}
function round(i: i32): i32 {
    var g: (i32) => Step = (x: i32): Step => make(x, i);
    var dropped: Step = make(2, 7);
    return run(make(4, i)) + walk(3, g) + first(g, 2) + run(g(2));
}
` + closureCallEnumMain

// The guard: a closure body returns an enum its environment still holds, so
// the result is shared. The body retains it and the caller's release only
// drops that count; keep is read after every call.
const closureCallEnumSharedSrc = `enum S { Done(i32), Next(i32, string) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    return Next(n, "ab" + "c");
}
function size(s: S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return w + t.len(); }
    }
    return -1;
}
function pass(s: S): S { return s; }
function first(g: (i32) => S, n: i32): i32 {
    var e: S = g(n);
    return size(e);
}
function spin(g: (i32) => S, n: i32): i32 {
    var cur: S = g(n);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        acc = acc + size(cur);
        cur = g(n + k);
        k = k + 1;
    }
    return acc + size(cur);
}
function round(i: i32): i32 {
    var keep: S = mk(i + 1);
    var g: (i32) => S = (x: i32): S => keep;
    var h: (i32) => S = (x: i32): S => pass(keep);
    var a: i32 = size(g(1)) + first(g, 2) + spin(g, 3) + first(h, 1) + size(h(2));
    return a + size(keep);
}
` + closureCallEnumMain

// Each closure body binds a fresh enum to a local and returns the local,
// which hands its count over ("ERETOWN:") rather than retaining it.
const closureCallEnumFreshLocalSrc = `enum S { Done(i32), Next(i32, string) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    return Next(n, "ab" + "c");
}
function run(s: S, g: (i32) => S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return t.len() + run(g(w), g); }
    }
    return -1;
}
function walk(n: i32, g: (i32) => S): i32 {
    var cur: S = g(n);
    var acc: i32 = 0;
    var guard: i32 = 0;
    while (guard < 100) {
        match (cur) {
            Done(v) => { return acc + v; },
            Next(w, t) => { acc = acc + t.len(); cur = g(w); }
        }
        guard = guard + 1;
    }
    return -1;
}
function round(i: i32): i32 {
    var k: i32 = i % 3;
    var g: (i32) => S = (x: i32): S => { var e: S = mk(x - 1); return e; };
    var c: (i32) => S = (x: i32): S => { var e: S = mk(x - 1 - k); return e; };
    return run(mk(4), g) + walk(4, g) + walk(3, c) + run(g(3), c);
}
` + closureCallEnumMain

// A capture-free lambda bound to a local that is only ever called is hoisted
// to a `__lam_` body and called by name; its returned fresh local hands its
// count over ("ERETOWN:").
const closureCallEnumLamLocalSrc = `enum S { Done(i32), Next(i32, string) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    return Next(n, "ab" + "c");
}
function size(s: S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return w + t.len(); }
    }
    return -1;
}
function round(i: i32): i32 {
    var f: (i32) => S = (x: i32): S => { var e: S = mk(x); return e; };
    return size(f(i)) + size(f(i + 1));
}
` + closureCallEnumMain

// No function value anywhere: a free function returning a local bound to a
// direct construction, held to a balanced census and a clean sanitizer.
const closureCallEnumNamedLocalSrc = `enum S { Done(i32), Next(i32, string) }
function mk(n: i32): S {
    if (n <= 0) { return Done(n); }
    var e: S = Next(n, "ab" + "c");
    return e;
}
function size(s: S): i32 {
    match (s) {
        Done(v) => { return v; },
        Next(w, t) => { return w + t.len(); }
    }
    return -1;
}
function round(i: i32): i32 {
    var a: S = mk(i);
    return size(a) + size(mk(i + 1));
}
` + closureCallEnumMain

const closureCallEnumMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Wants are interpreter-confirmed. astCensus pins the AST lowering's census
// where it is not balanced: the guard leaks the enum each closure captures,
// which a closure environment never releases (#10587). The census is the
// same without the retained returns.
var closureCallEnumCases = []struct {
	name      string
	src       string
	want      int
	astCensus [2]int64
}{
	{"issue", closureCallEnumIssueSrc, 36, [2]int64{}},
	{"string", closureCallEnumStringSrc, 85, [2]int64{}},
	{"array", closureCallEnumArraySrc, 48, [2]int64{}},
	{"closure", closureCallEnumClosureSrc, 22, [2]int64{}},
	{"shared", closureCallEnumSharedSrc, 86, [2]int64{30, 20}},
	{"fresh_local", closureCallEnumFreshLocalSrc, 85, [2]int64{}},
	{"lam_local", closureCallEnumLamLocalSrc, 60, [2]int64{}},
	{"named_local", closureCallEnumNamedLocalSrc, 60, [2]int64{}},
}

func TestSelfHostClosureCallEnumX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureCallEnumCases {
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				checkClosureCallEnumCensus(t, stderr, tc.astCensus, lw.name)
				stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1", lw.env)
				balanced := lw.name != "ast" || tc.astCensus == [2]int64{}
				if exit != tc.want || forArrStructSanitizerFault(stderr, balanced) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer finding\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostClosureCallEnumArm64(t *testing.T) {
	checkClosureCallEnum(t, "arm64-linux")
}

func TestSelfHostClosureCallEnumWasm(t *testing.T) {
	checkClosureCallEnum(t, "wasm32-wasi")
}

func checkClosureCallEnum(t *testing.T, target string) {
	cli := buildSelfHostCLI(t)
	for _, tc := range closureCallEnumCases {
		for _, lw := range vblockClosureBoth {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				checkClosureCallEnumCensus(t, stderr, tc.astCensus, lw.name)
			})
		}
	}
}

// checkClosureCallEnumCensus: balanced, or on the AST lowering exactly the
// pinned allocs and frees when the case has a pin.
func checkClosureCallEnumCensus(t *testing.T, stderr string, pin [2]int64, lowering string) {
	t.Helper()
	if lowering != "ast" || pin == [2]int64{} {
		assertBalancedCensus(t, stderr)
		return
	}
	summary := leakSummaryLine(stderr)
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if got := [2]int64{allocs, frees}; got != pin {
		t.Errorf("%s, want allocs=%d frees=%d", summary, pin[0], pin[1])
	}
}
