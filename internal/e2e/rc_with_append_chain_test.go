package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// `xs.with(i, v).append(w)` with `xs` still live: the `.with` is forced onto
// its copy path, so the append's receiver is a fresh buffer only the chain
// holds, and the append releases it like any other owned receiver.
func TestAppendOntoAWithCopyReleasesTheCopy(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"scalar-elements", `function f(i: i32): i32 {
    var xs: i32[] = [1, 2, 3];
    var zs: i32[] = xs.with(2, i).append(8);
    return zs.len() + xs[1];
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 3) { acc = acc + f(i); i = i + 1; } return acc; }`, 18},
		{"string-elements", `function mk(a: string): string { return a + "!"; }
function f(i: i32): i32 {
    var xs: string[] = [mk("a string long enough for the heap"), mk("another heap string")];
    var zs: string[] = xs.with(1, mk("a replacement heap string")).append(mk("an appended heap string"));
    return zs.len() + xs[1].len();
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 3) { acc = acc + f(i); i = i + 1; } return acc % 200; }`, 69},
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
