package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A string local the exit sweep cannot prove it owns, but whose every store
// was counted, is released without being freed, as every other ineligible type
// is; and a parameter stored into such a local is a counted store, so the
// caller keeps its own release (#10117). One bound without a retain holds
// nothing to release. Each shape
// builds its strings by concatenation so they are heap blocks, and runs
// several calls so a per-call leak shows as a count.
func TestIneligibleStringLocalReleasesItsReference(t *testing.T) {
	const mk = `function mkstr(a: string): string { return a + "!"; }
`
	cases := []struct {
		name, src string
		want      int
	}{
		{"assigned-in-a-branch", mk + `function tag(src: string, i: i32): i32 {
    var x: string = src;
    var out: string = "";
    if (i % 2 == 0) { out = x; }
    return out.len() + i;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a string long enough to defeat the small-string optimisation");
        acc = acc + tag(line, i);
        i = i + 1;
    }
    return acc - 100;
}`, 28},
		{"assigned-at-top-level", mk + `function tag(src: string): i32 {
    var x: string = src;
    var out: string = "";
    out = x;
    return out.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var line: string = mkstr("another string long enough to live on the heap");
        acc = acc + tag(line);
        i = i + 1;
    }
    return acc - 100;
}`, 41},
		{"returned", mk + `function pick(src: string, i: i32): string {
    var out: string = "none";
    if (i % 2 == 0) { out = src; }
    return out;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a third string long enough to live on the heap");
        acc = acc + pick(line, i).len();
        i = i + 1;
    }
    return acc - 50;
}`, 52},
		// `s` is bound to the block's tail value with no retain, so it holds
		// no reference of its own: releasing it at exit is a use-after-free
		// once `joined` has freed the buffer.
		{"bound-to-a-block-tail", mk + `function tail(i: i32): i32 {
    var a: string = mkstr("a string long enough to live on the heap");
    var s: string = if (i >= 0) { var joined = a + "?"; joined } else { "" };
    return s.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + tail(i); i = i + 1; }
    return acc - 100;
}`, 68},
		{"literals-only", mk + `function label(i: i32): i32 {
    var out: string = "odd";
    if (i % 2 == 0) { out = "even"; }
    return out.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a fourth string long enough to live on the heap");
        acc = acc + label(i) + line.len();
        i = i + 1;
    }
    return acc - 150;
}`, 56},
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
