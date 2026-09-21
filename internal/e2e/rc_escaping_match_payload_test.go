package e2e

import "testing"

// A match arm may hand its payload OUT of the arm, and the scrutinee still
// has to be reclaimed.
//
// Both fresh-call-scrutinee reclaims — the pair-form payload release and the
// boxed-scrutinee deep drop — used to require the arm's pointer bindings be
// CONFINED: read through, never stored. `Some(c) => { chunk = c; }` stores,
// so the whole match was refused and the payload the callee had just
// allocated was abandoned, once per match. In a serve loop that is unbounded
// growth: it is tcp_serve's per-request recv buffer, ~5.4 KB a request with
// no plateau (#8003).
//
// Confinement was sufficient but not necessary. The store copies the pointer
// into a local under an alias inc, so the destination carries a reference of
// its own and the reclaim takes the payload down to that one — which is what
// bindingReleasableInArm asks instead.
//
// The control is the spelling: binding the scrutinee to a local first
// (`var r = mk(i); match (r)`) always reclaimed, and was the workaround
// measured on the issue. Both spellings balancing is the property that makes
// the workaround unnecessary; a green `direct` with a red `bound` would mean
// the reclaim moved rather than widened.
func TestEscapingMatchPayloadIsReclaimed(t *testing.T) {
	// Option[u8[]] returns in PAIR form (tag + payload in registers, no box),
	// so what leaks is the payload array itself.
	pairForm := func(arm, scrut string) string {
		return `
function mk(i: i32): Option[u8[]] {
    var b: u8[] = [];
    var j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return None; }
    return Some(b);
}

function round(i: i32): i32 {
    var chunk: u8[] = [];
    var missing: boolean = false;
    ` + scrut + `
        Some(c) => { ` + arm + ` },
        None => { missing = true; },
    }
    if (missing) { return 0; }
    return chunk.len();
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`
	}
	// A two-payload variant is too wide for the pair-form ABI, so this one
	// goes through a heap box: the leak is the box AND the array it holds,
	// and the reclaim is the deep drop that releases both.
	boxed := `
enum Chunk { Got(u8[], i32), Nope }

function mk(i: i32): Chunk {
    var b: u8[] = [];
    var j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return Nope; }
    return Got(b, i);
}

function round(i: i32): i32 {
    var chunk: u8[] = [];
    var missing: boolean = false;
    match (mk(i)) {
        Got(c, n) => { chunk = c; },
        Nope => { missing = true; },
    }
    if (missing) { return 0; }
    return chunk.len();
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

	shapes := []struct{ name, src string }{
		{"pair_payload_to_outer_local", pairForm("chunk = c;", "match (mk(i)) {")},
		{"boxed_payload_to_outer_local", boxed},
		{"bound_scrutinee_control", pairForm("chunk = c;", "var r: Option[u8[]] = mk(i);\n    match (r) {")},
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
					// 71 is the program's own answer; wasm reports 0.
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
