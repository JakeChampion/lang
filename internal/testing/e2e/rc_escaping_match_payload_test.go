package e2e

import "testing"

func TestEscapingMatchPayloadIsReclaimed(t *testing.T) {
	pairForm := func(arm, scrut string) string {
		return `
function mk(i: i32): Option[u8[]] {
    let b: u8[] = [];
    let j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return None; }
    return Some(b);
}

function round(i: i32): i32 {
    let chunk: u8[] = [];
    let missing: boolean = false;
    ` + scrut + `
        Some(c) => { ` + arm + ` },
        None => { missing = true; },
    }
    if (missing) { return 0; }
    return chunk.len();
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`
	}
	boxed := `
enum Chunk { Got(u8[], i32), Nope }

function mk(i: i32): Chunk {
    let b: u8[] = [];
    let j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return Nope; }
    return Got(b, i);
}

function round(i: i32): i32 {
    let chunk: u8[] = [];
    let missing: boolean = false;
    match (mk(i)) {
        Got(c, n) => { chunk = c; },
        Nope => { missing = true; },
    }
    if (missing) { return 0; }
    return chunk.len();
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

	shapes := []struct{ name, src string }{
		{"pair_payload_to_outer_local", pairForm("chunk = c;", "match (mk(i)) {")},
		{"boxed_payload_to_outer_local", boxed},
		{"bound_scrutinee_control", pairForm("chunk = c;", "let r: Option[u8[]] = mk(i);\n    match (r) {")},
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
			for _, shape := range shapes {
				t.Run(shape.name, func(t *testing.T) {
					_, stderr, code := tc.run(t, shape.src)
					if code != 71 && code != 0 {
						t.Fatalf("exit=%d, want the program's own 71 (or wasm's 0) — "+
							"a non-zero __rc_underflow_count() returns 99", code)
					}
					allocs, frees, live := parseLeakCheckLine(t, stderr)
					if allocs == 0 {
						t.Fatalf("no allocations — the loop is not running")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — "+
							"the arm's payload is abandoned once per match (#8003)",
							allocs, frees, live)
					}
				})
			}
		})
	}
}
