package e2e

import (
	"strings"
	"testing"
)

// A call through a function VALUE releases its argument temps and, under a
// match, its result box, whatever the callee expression is: a struct field,
// an array element, a call's result or a capture, as well as the named local
// that already did. Each case runs four calls, and each returns 6.
func TestIndirectCallThroughAnyCalleeReleasesItsTemps(t *testing.T) {
	const prelude = `struct C { value: i32 }
struct S { name: string, run: (C) => i32 }
struct R { name: string, run: (i32) => Result[C, string] }
function val(c: C): i32 { return c.value; }
function mk(n: i32): Result[C, string] { return Ok(C { value: n }); }
function pick(): (C) => i32 { return val; }
`
	cases := []struct{ name, src string }{
		{"field-arg", `function main(): i32 {
    var st: S = S { name: "s", run: val };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + st.run(C { value: i }); i = i + 1; }
    return acc;
}`},
		{"field-result", `function main(): i32 {
    var r: R = R { name: "r", run: mk };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (r.run(i)) { Ok(n) => { acc = acc + n.value; }, Err(e) => { acc = acc + 100; } }
        i = i + 1;
    }
    return acc;
}`},
		{"element-arg", `function main(): i32 {
    var fs: ((C) => i32)[] = [val];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + fs[0](C { value: i }); i = i + 1; }
    return acc;
}`},
		{"chained-arg", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + pick()(C { value: i }); i = i + 1; }
    return acc;
}`},
		{"captured-arg", `function apply(g: (C) => i32, k: i32): i32 {
    var h: (i32) => i32 = (x: i32) => g(C { value: x });
    return h(k);
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + apply(val, i); i = i + 1; }
    return acc;
}`},
	}
	for _, c := range cases {
		src := prelude + c.src
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, src, 6, runSanitizeX86_64)
		})
		t.Run("arm64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, src, 6, runSanitizeArm64)
		})
		t.Run("wasm-leakcheck/"+c.name, func(t *testing.T) {
			stdout, stderr, _ := runLeakCheckWasm(t, src, true)
			if got := strings.TrimSpace(stdout); got != "6" {
				t.Fatalf("result=%q, want 6\n%s", got, stderr)
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
