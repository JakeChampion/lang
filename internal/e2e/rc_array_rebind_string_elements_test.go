package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A local rebound from a call that mentions it, `pg = emit(pg, inner)`, frees
// the buffer it gave up when that buffer holds counted elements. The array
// rebind has an identity guard, so an element type that reaches a string no
// longer drops it to the flat dec, which never frees. Two shapes: the one
// std/regex's program emitter has, and one whose callee sometimes returns its
// argument unchanged, which is the case the guard is for.
func TestArrayRebindWithStringElementsFreesTheOldBuffer(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"wrapped-recursion", `enum N { C(i32), S(N[]), W(N) }
enum I { IC(i32), IS(string) }
function emit(prog: I[], n: N): I[] {
    match (n) {
        C(c) => { return prog.append(IC(c)); },
        S(xs) => {
            var ps: I[] = prog;
            var k: i32 = 0;
            while (k < xs.len()) { ps = emit(ps, xs[k]); k = k + 1; }
            return ps;
        },
        W(inner) => {
            var pg: I[] = prog.append(IC(0));
            pg = emit(pg, inner);
            return pg;
        },
    }
}
function main(): i32 {
    var start: I[] = [];
    var out: I[] = emit(start, W(S([C(1), C(2)])));
    return out.len();
}`, 3},
		{"callee-returns-its-argument", `enum N { C(string), Same, W(N), S(N[]) }
function emit(prog: string[], n: N): string[] {
    match (n) {
        C(s) => { return prog.append(s); },
        Same => { return prog; },
        W(inner) => {
            var pg: string[] = prog.append(mk("a string long enough to live on the heap"));
            pg = emit(pg, inner);
            return pg;
        },
        S(xs) => {
            var ps: string[] = prog;
            var k: i32 = 0;
            while (k < xs.len()) { ps = emit(ps, xs[k]); k = k + 1; }
            return ps;
        },
    }
}
function mk(a: string): string { return a + "!"; }
function main(): i32 {
    var total: i32 = 0;
    var r: i32 = 0;
    while (r < 5) {
        var start: string[] = [];
        var t: N = W(S([W(Same), Same, W(C(mk("another string long enough for the heap"))), W(W(Same))]));
        var out: string[] = emit(start, t);
        total = total + out.len();
        r = r + 1;
    }
    return total;
}`, 30},
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
