package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A local bound to the result of a call through a function value owns that
// result whatever the callee expression is, so it is released at scope exit
// even when an argument is borrowed. The field case is #10149's program.
func TestIndirectCallBoundResultIsReleased(t *testing.T) {
	cases := []struct {
		name string
		want int
		src  string
	}{
		{"struct-field", 42, `struct Txn { id: i32 }
struct Out { v: i32 }
struct Stage { name: string, run: (string, Txn) => Out }
function step(s: string, t: Txn): Out { return Out { v: s.len() + t.id }; }
function run_with(st: Stage, t: Txn): i32 {
    var o: Out = st.run(st.name, t);
    return o.v;
}
function main(): i32 {
    var st: Stage = Stage { name: "abc", run: step };
    return run_with(st, Txn { id: 4 }) * 6;
}`},
		{"element-and-call-result", 21, `struct Out { v: i32, tag: string }
function mk(s: string, k: i32): Out { return Out { v: s.len() + k, tag: "t" }; }
function pick(): (string, i32) => Out { return mk; }
function via_element(fs: ((string, i32) => Out)[], s: string, k: i32): i32 {
    var o: Out = fs[0](s, k);
    return o.v;
}
function via_call(s: string, k: i32): i32 {
    var o: Out = pick()(s, k);
    return o.v;
}
function main(): i32 {
    var fs: ((string, i32) => Out)[] = [mk];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { acc = acc + via_element(fs, "ab", i) + via_call("abc", i); i = i + 1; }
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
