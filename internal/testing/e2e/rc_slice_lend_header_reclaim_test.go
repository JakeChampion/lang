package e2e

import "testing"

func TestSliceLendHeaderIsReclaimed(t *testing.T) {
	src := func(param string) string {
		return `
function total(src: ` + param + `, n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}

function mk(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, (i % 7) as u8); i = i + 1; }
    return a;
}

function round(i: i32): i32 {
    let b: u8[] = mk(8);
    return total(b, 8) + i % 3;
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`
	}
	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", runLeakCheckX86_64},
		{"arm64", runLeakCheckArm64},
		{"wasm", func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, shape := range []struct{ name, param string }{
				{"lent_view", "[u8]"},
				{"owned_array_control", "u8[]"},
			} {
				t.Run(shape.name, func(t *testing.T) {
					_, stderr, code := tc.run(t, src(shape.param))
					if code != 20 && code != 0 {
						t.Fatalf("exit=%d, want the program's own 20 (or wasm's 0) — "+
							"a non-zero __rc_underflow_count() returns 99", code)
					}
					allocs, frees, live := parseLeakCheckLine(t, stderr)
					if allocs == 0 {
						t.Fatalf("no allocations — the loop is not running")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — "+
							"the lend's (data, len) header is stranded once per call (#8502)",
							allocs, frees, live)
					}
				})
			}
		})
	}
}
