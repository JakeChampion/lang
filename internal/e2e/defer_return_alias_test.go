package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A defer that reassigns the local a function is returning runs after the
// value is captured, so the caller sees the value as it was at the return
// (#10027). The return's transfer retain comes before the defers: without it
// the deferred reassignment saw a unique cell and reused it in place, so
// `record` answered the replacement and the larger program crashed.
var deferReturnAliasCases = []struct {
	name string
	src  string
	want int
}{
	{"record", `struct P { a: i32 }
function build(): P {
    var p: P = P { a: 3 };
    defer p = P { a: 9 };
    return p;
}
function main(): i32 { return build().a; }`, 3},
	{"mixed", `struct P { a: i32, s: string }
enum Shape { Dot, Box(i32) }
function record(n: i32): P {
    var p: P = P { a: n, s: "p" };
    defer p = P { a: 0, s: "" };
    return p;
}
function pair(n: i32): (i32, string) {
    var t: (i32, string) = (n, "one");
    defer t = (0, "");
    return t;
}
function variant(n: i32): Shape {
    var s: Shape = Shape.Dot;
    defer s = Shape.Box(n);
    if (n % 2 == 0) { return Shape.Box(n); }
    return s;
}
function maybe(n: i32): Option[P] {
    var log: i32 = 0;
    defer log = log + 1;
    if (n < 0) { return None; }
    return Some(P { a: n, s: "m" });
}
function risky(fail: boolean): Result[i32, i32] {
    var code: i32 = 0;
    errdefer { code = code + 7; }
    if (fail) { return Err(code + 1); }
    return Ok(50);
}
function two_defers(n: i32): P {
    var p: P = P { a: n, s: "x" };
    defer { p = P { a: 0, s: "" }; }
    var q: P = p;
    defer q = P { a: 5, s: "q" };
    return P { a: p.a + q.a, s: p.s + q.s };
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var pr: (i32, string) = pair(i);
        t = t + record(i).a + pr.0 + pr.1.len();
        match (variant(i)) { Shape.Dot => { t = t + 1; }, Shape.Box(v) => { t = t + v; } }
        match (maybe(i - 100)) { Some(p) => { t = t + p.a + p.s.len(); }, None => { t = t + 2; } }
        match (risky(i % 3 == 0)) { Ok(v) => { t = t + v; }, Err(e) => { t = t + e; } }
        t = t + two_defers(i).s.len();
        i = i + 1;
    }
    return t % 97;
}`, 8},
}

func TestDeferReassignKeepsTheReturnedValue(t *testing.T) {
	for _, tc := range deferReturnAliasCases {
		t.Run(tc.name+"/interp", func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
		t.Run(tc.name+"/x86-64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckX86_64(t, tc.src)
			checkDeferReturnCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/arm64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckArm64(t, tc.src)
			checkDeferReturnCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			stdout, stderr, exit := runLeakCheckWasm(t, tc.src, false)
			if exit != 0 || strings.TrimSpace(stdout) != strconv.Itoa(tc.want) {
				t.Fatalf("exit %d stdout %q, want exit 0 printing %d; stderr: %s", exit, stdout, tc.want, stderr)
			}
			a, f, live := parseWasmLeakCheckLine(t, stderr)
			if a != f || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", a, f, live)
			}
		})
	}
}

func checkDeferReturnCensus(t *testing.T, want int, stderr string, exit int) {
	t.Helper()
	if exit != want {
		t.Fatalf("exit %d, want %d; stderr: %s", exit, want, stderr)
	}
	a, f, live := parseLeakCheckLine(t, stderr)
	if a != f || live != 0 {
		t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", a, f, live)
	}
}
