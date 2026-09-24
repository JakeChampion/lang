package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A fresh owned enum box is freed after the match that consumed it
// (reclaimableMatchScrutinee, #6417): a `_` payload position is eligible, and
// so is a NAMED payload moved into a variant construction (#9180).

// A boxed enum whose Err-side payload is a string, matched directly on the
// call.
const matchCallWildcardSrc = `enum E { A(i32), B(string) }
function mk(i: i32): E {
    if (i < 0) { return B("x"); }
    return A(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (mk(i)) { A(a) => { acc = acc + a; }, B(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The Result spelling of the same shape.
const matchCallResultSrc = `function make(i: i32): Result[i32, string] {
    if (i < 0) { return Err("neg"); }
    return Ok(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (make(i)) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// Control: an all-scalar boxed enum is eligible and must stay so.
const matchCallScalarSrc = `enum E { A(i32, i32, i32), B(i32) }
function mk(i: i32): E {
    if (i < 0) { return B(0); }
    return A(i, i + 1, i + 2);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (mk(i)) { A(a, b, c) => { acc = acc + a + b + c; }, B(z) => { acc = acc + z; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The string payload is bound to a real name and re-wrapped, so the binding
// outlives the arm: the new Err takes the payload's reference and the old box
// goes shell-only. Every other iteration takes the Err arm and reads the
// payload back after the free.
const matchCallBoundPayloadSrc = `function make(i: i32): Result[i32, string] {
    if (i % 2 == 1) { return Err("neg" + "ative"); }
    return Ok(i);
}
function step(i: i32): Result[i32, string] {
    match (make(i)) { Ok(v) => { return Ok(v + 1); }, Err(e) => { return Err(e); } }
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (step(i)) { Ok(x) => { acc = acc + x; }, Err(e) => { acc = acc + e.len(); } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// leakCounts compiles src with the leak census on, runs it, and returns the
// summary's three numbers after asserting the exit code.
func leakCounts(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
	t.Helper()
	stdout, stderr, code := runLeakCheckX86_64(t, src)
	if code != wantExit {
		t.Fatalf("%s: exit=%d, want %d (stdout %q, stderr %q)", name, code, wantExit, stdout, stderr)
	}
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
	}
	var allocs, frees, live int64
	for _, tok := range strings.Fields(summary) {
		for prefix, dst := range map[string]*int64{"allocs=": &allocs, "frees=": &frees, "live_bytes=": &live} {
			if strings.HasPrefix(tok, prefix) {
				v, err := strconv.ParseInt(strings.TrimPrefix(tok, prefix), 10, 64)
				if err != nil {
					t.Fatalf("%s: parse %q: %v", name, tok, err)
				}
				*dst = v
			}
		}
	}
	if allocs == 0 {
		t.Fatalf("%s allocated nothing — the probe is not exercising the boxed path", name)
	}
	return allocs, frees, live
}

func TestX86_64MatchCallScrutineeReclaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"wildcard_string_payload", matchCallWildcardSrc, 72},
		{"result_err_wildcard", matchCallResultSrc, 72},
		{"all_scalar_control", matchCallScalarSrc, 65},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := leakCounts(t, tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("%s: live_bytes=%d (allocs=%d frees=%d), want 0 — one unfreed scrutinee box per iteration",
					tc.name, live, allocs, frees)
			}
		})
	}

	t.Run("bound_payload_moved_into_construction", func(t *testing.T) {
		allocs, frees, live := leakCounts(t, "bound_payload", matchCallBoundPayloadSrc, 61)
		if live != 0 {
			t.Errorf("bound_payload: live_bytes=%d (allocs=%d frees=%d), want 0 — the re-wrapped payload's old box is not reclaimed",
				live, allocs, frees)
		}
	})
}
