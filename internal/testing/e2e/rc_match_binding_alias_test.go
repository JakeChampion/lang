package e2e

import "testing"

func TestMatchBindingAliasIsCancelled(t *testing.T) {
	mk := `
function mk(i: i32): Option[u8[]] {
    let b: u8[] = [];
    let j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return None; }
    return Some(b);
}
`
	boundScrutinee := mk + `
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let r: Option[u8[]] = mk(i);
        match (r) {
            Some(c) => { let kept: u8[] = c; total = total + kept.len(); },
            None => { },
        }
        i = i + 1;
    }
    if (total != 320) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

	directScrutinee := mk + `
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        match (mk(i)) {
            Some(c) => { let kept: u8[] = c; total = total + kept.len(); },
            None => { },
        }
        i = i + 1;
    }
    if (total != 320) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

	outerLocalControl := mk + `
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    let side: u8[] = [1, 2, 3];
    while (i < 50) {
        match (mk(i)) {
            Some(c) => { let kept: u8[] = side; total = total + kept.len() + c.len(); },
            None => { },
        }
        i = i + 1;
    }
    if (total != 440) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

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
			for _, shape := range []struct{ name, src string }{
				{"bound_scrutinee", boundScrutinee},
				{"direct_scrutinee", directScrutinee},
				{"outer_local_control", outerLocalControl},
			} {
				t.Run(shape.name, func(t *testing.T) {
					_, stderr, code := tc.run(t, shape.src)
					if code != 0 {
						t.Fatalf("exit=%d, want 0 — 91 is a wrong total (the cancelled alias "+
							"read the wrong bytes), 99 a non-zero __rc_underflow_count()", code)
					}
					allocs, frees, live := parseLeakCheckLine(t, stderr)
					if allocs == 0 {
						t.Fatalf("no allocations — the loop is not running")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — the "+
							"arm's `let` takes a reference nothing gives back (#9923)",
							allocs, frees, live)
					}
				})
			}
		})
	}
}
