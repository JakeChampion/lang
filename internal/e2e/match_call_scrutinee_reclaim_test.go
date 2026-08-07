package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// --- match-on-call scrutinee reclaim (#6417) -------------------------------
//
// `reclaimableMatchScrutinee` frees a fresh owned enum box after the match
// that consumed it. It refused whenever ANY arm binding position had a
// pointer TYPE — but a boxed enum only exists when some variant's payload is
// pointer-shaped, so that clause rejected essentially every boxed `Result`,
// including the ubiquitous `Err(_)` idiom where nothing is bound at all.
//
// A `_` position extracts nothing, so no binding can outlive the post-match
// free, and the generated per-enum drop releases that payload variant-aware
// and deep. Those positions are now exempt.
//
// Both directions are pinned. A payload bound to a real NAME stays refused —
// that binding could alias the box past the free — and the leak is the safe
// direction there.

// A boxed enum whose Err-side payload is a string, matched directly on the
// call. This leaked one box per iteration (frees=0) before the fix.
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

// The Result spelling of the same shape — the form #6417 was filed against.
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

// Control: an all-scalar boxed enum was already eligible and must stay so —
// the new exemption must not disturb the path it sits beside.
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

// HAZARD: the string payload is bound to a real name AND re-wrapped, so the
// binding outlives the arm. Must stay REFUSED — i.e. must still leak. The
// leak is the safe direction; freeing here could strand or double-release the
// payload the binding carries onward.
const matchCallBoundPayloadSrc = `function make(i: i32): Result[i32, string] {
    if (i < 0) { return Err("neg"); }
    return Ok(i);
}
function step(i: i32): Result[i32, string] {
    match (make(i)) { Ok(v) => { return Ok(v + 1); }, Err(e) => { return Err(e); } }
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (step(i)) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
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
		{"wildcard_string_payload", matchCallWildcardSrc, 65},
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

	t.Run("bound_payload_stays_refused", func(t *testing.T) {
		allocs, frees, live := leakCounts(t, "bound_payload", matchCallBoundPayloadSrc, 57)
		if live <= 0 {
			t.Errorf("a NAMED pointer payload binding now reclaims (allocs=%d frees=%d live=%d). That is only "+
				"safe if the binding is proven confined to its arm — it is re-wrapped into the returned Err "+
				"here, so it outlives the free. Re-read reclaimableMatchScrutinee before taking this green.",
				allocs, frees, live)
		}
	})
}
