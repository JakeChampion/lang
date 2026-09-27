package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A value block `{ stmts; tail }` whose tail names a local yields an alias of
// that local, and the exit sweep still releases the local, so every consumer
// has to retain the tail as it would the bare name (#10435). Without the
// retain `return { var q = …; q }` handed back a freed buffer, and an array
// bound from a block was released by the block local's reinit drop while the
// binding still held it.
func TestValueBlockTailAliasIsRetained(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"issue-10435", `struct P { x: i32, y: i32 }
function mk(j: i32): i32[] { return { var q = [j, j * 2]; q = q.append(3); q }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 2) {
        var c: P[] = { var q = [P { x: 5, y: j }]; q };
        var m = mk(j);
        t = t + c[0].y * 10 + m[2];
        j = j + 1;
    }
    return t;
}`, 16},
		{"every-position", `struct P { x: i32, y: i32 }
struct Box { items: i32[] }
function mk(j: i32): i32[] { return { var q = [j, j * 2]; q = q.append(3); q }; }
function pick(b: boolean, j: i32): i32[] {
    return if (b) { var q = [j, 7]; q = q.append(4); q } else { var r = [j, 8]; r = r.append(5); r };
}
function sum(xs: i32[]): i32 { var s = 0; var i = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var c: P[] = { var q = [P { x: 5, y: j }]; q };
        var m = mk(j);
        var p = pick(j == 1, j);
        var s = sum({ var w = [j, 1]; w = w.append(1); w });
        var bx = Box { items: { var v = [j]; v = v.append(2); v } };
        var nest = [{ var e = [j, 9]; e }];
        var ps = { var inner = { var z = [P { x: j, y: 1 }]; z }; inner };
        t = t + c[0].y * 10 + m[2] + p[2] + s + bx.items[1] + nest[0][1] + ps[0].x;
        j = j + 1;
    }
    return t;
}`, 98},
		{"string-struct-tuple", `struct P { x: i32, y: i32 }
function name(j: i32): string { return { var s = "ab"; s = s + "cd"; s }; }
function pt(j: i32): P { return { var p = P { x: j, y: 2 }; p }; }
function main(): i32 {
    var t = 0;
    var j = 0;
    while (j < 3) {
        var s: string = { var a = name(j); a = a + "e"; a };
        var p = if (j > 0) { var q = pt(j); q } else { var r = P { x: 9, y: 9 }; r };
        var tp = { var u = (j, [j, 1]); u };
        var k = { var w = s; w };
        t = t + s.len() + k.len() + p.x + tp.1[1];
        j = j + 1;
    }
    return t;
}`, 45},
	}
	for _, c := range cases {
		if got := runInterpByte(t, c.src); got != c.want {
			t.Fatalf("%s: interp = %d, want %d", c.name, got, c.want)
		}
		assertBackendsAgreeWithInterp(t, c.name, c.src)
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
