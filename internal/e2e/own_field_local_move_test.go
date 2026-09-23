package e2e

import (
	"strings"
	"testing"
)

// #10084 — a field read out of an owned struct into a local, `var a = s.a`
// or `var S { a, b, n } = s`, is moved out of the box when nothing after it
// reads the field again. The local is then the array's only holder, so a
// `.with` on it writes in place instead of copying the whole buffer.
//
// The two loops print how many allocations their 1000 updates made: none
// for the `var` form, whose spread reuses the box, and one struct each for
// the pattern form. Each copied the 256-element array as well before the
// move. The shared case checks the other arm of the runtime test: with the
// box aliased, the field is retained, not emptied, and the alias keeps its
// values.
const ownFieldLocalMoveSrc = `import "std/i64";

struct S { a: i64[], b: string[], n: i32 }

function via_var(own s: S, i: i32): S {
    var a: i64[] = s.a;
    a = a.with(i, a[i] + 1 as i64);
    return S { ...s, a: a, n: s.n + 1 };
}

function via_pattern(own s: S, i: i32): S {
    var S { a, b, n } = s;
    a = a.with(i, a[i] + 1 as i64);
    return S { a: a, b: b, n: n + 1 };
}

function fresh(): S {
    var xs: i64[] = [];
    var i: i32 = 0;
    while (i < 256) {
        xs = xs.append(0 as i64);
        i = i + 1;
    }
    return S { a: xs, b: ["x", "y"], n: 0 };
}

function main(): i32 {
    var s: S = fresh();
    var before: i64 = __heap_alloc_count();
    var i: i32 = 0;
    while (i < 1000) {
        s = via_var(s, i & 255);
        i = i + 1;
    }
    var mid: i64 = __heap_alloc_count();
    i = 0;
    while (i < 1000) {
        s = via_pattern(s, i & 255);
        i = i + 1;
    }
    var after: i64 = __heap_alloc_count();
    var t: S = fresh();
    var keep: S = t;
    t = via_var(t, 3);
    t = via_pattern(t, 3);
    if (keep.a[3] != 0 as i64 || keep.n != 0) { return 1; }
    if (t.a[3] != 2 as i64 || t.n != 2) { return 1; }
    if (s.a[0] != 8 as i64 || s.n != 2000) { return 1; }
    print((mid - before).to_string());
    print((after - mid).to_string());
    return 0;
}`

func TestOwnFieldLocalMove(t *testing.T) {
	for _, tc := range []struct {
		name   string
		run    func(*testing.T, string) (string, string, int)
		counts bool
	}{
		{"x86_64", runLeakCheckX86_64, true},
		{"arm64", runLeakCheckArm64, true},
		// The component's heap does not count allocations the same way, so
		// only the values and the census are checked there.
		{"wasm", func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := tc.run(t, ownFieldLocalMoveSrc)
			if code != 0 {
				t.Fatalf("exit=%d, want 0: a moved field was read back empty or an alias changed\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
			}
			if !tc.counts {
				return
			}
			lines := strings.Fields(stdout)
			if len(lines) != 2 || lines[0] != "0" || lines[1] != "1000" {
				t.Errorf("allocations over 1000 updates = %q, want [0 1000]: the field was copied rather than moved", lines)
			}
		})
	}
}
