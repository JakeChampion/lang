package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Rebound Option siblings: arr-of-arr, struct, tuple payloads (#6127) -----
//
// #6225 closed the flat-scalar-array kind and noted that "all three existing
// Option siblings carry the same blunt `reassigned` exclusion, so rebound
// arr-of-arr, struct and tuple payloads leak for the same reason." Measured, and
// they did — every rebound form freed NOTHING at all:
//
//	                  single bind   declared in loop   REBOUND
//	Option[(i32,i32[])]         0              12000     60000  frees=0
//	Option[P{xs:i32[]}]      4000              12800     64000  frees=0
//	Option[i32[][]]             0              15200     76000  frees=0
//
// The collectors are structurally identical and differ only in which freshness
// predicate they apply, so the fix is one relaxation applied three times:
// a reassigned name is admitted when EVERY rebind is itself fresh, and the
// StmtAssign path — which the family had never used, because refusing reassigned
// names meant it could only ever reclaim at a `var` re-declaration — releases the
// superseded chain at the depth that payload kind needs.
//
// The per-rebind walk that #6225 wrote for the flat kind is now shared by all
// four (opt_rebinds_all_fresh + opt_assign_is_fresh), rather than copied a
// fourth time.
//
// These assert allocs == frees alongside live_bytes == 0: frees > allocs is a
// double free, frees < allocs an unclaimed box, and they mean different bugs.

// TestSelfHostOptSiblingRebindReclaimX86_64 — each rebound Option sibling
// reclaims every superseded chain, and the single-bind forms stay as they were.
func TestSelfHostOptSiblingRebindReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d", name, exit, wantExit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	balanced := func(t *testing.T, name, src string, wantExit int) {
		t.Helper()
		allocs, frees, live := counts(t, name, src, wantExit)
		if live != 0 {
			t.Errorf("%s: live_bytes=%d, want 0 — the leak scales with the iteration "+
				"count, so any nonzero here is unbounded", name, live)
		}
		if allocs != frees {
			t.Errorf("%s: allocs=%d frees=%d — must balance exactly", name, allocs, frees)
		}
	}

	t.Run("opttup_rebound", func(t *testing.T) {
		balanced(t, "opttup_rebound", `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var k: i32 = 0;
    while (k < 4) { o = Some((k, [k, k + 1])); k = k + 1; }
    match (o) { Some(t) => { acc = t.0 + t.1.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 2)
	})

	t.Run("optstruct_rebound", func(t *testing.T) {
		balanced(t, "optstruct_rebound", `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    var k: i32 = 0;
    while (k < 4) { o = Some(P { xs: [k, k + 1], n: k }); k = k + 1; }
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 2)
	})

	t.Run("optarrarr_rebound", func(t *testing.T) {
		balanced(t, "optarrarr_rebound", `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    var k: i32 = 0;
    while (k < 4) { o = Some([[k, k + 1], [k + 2]]); k = k + 1; }
    match (o) { Some(g) => { acc = g.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 34)
	})

	t.Run("opttup_single_bind_unchanged", func(t *testing.T) {
		// Never reassigned, so it is still the consuming-match analysis's. If the
		// widened collector also claimed it, its chain would be freed twice.
		balanced(t, "opttup_single", `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    match (o) { Some(t) => { acc = t.0 + t.1.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 4)
	})

	t.Run("optarrarr_single_bind_unchanged", func(t *testing.T) {
		balanced(t, "optarrarr_single", `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    match (o) { Some(g) => { acc = g.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 34)
	})

	t.Run("optstruct_single_bind_no_double_free", func(t *testing.T) {
		// This one is NOT balanced and was not before: a single-bind
		// Option[<struct-with-array>] leaks 4000 over 100 rounds, one block a round,
		// a residue outside this change. What matters here is the direction — frees
		// must not EXCEED allocs, which is what a wrongly-widened credit would do.
		allocs, frees, _ := counts(t, "optstruct_single", `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 4)
		if frees > allocs {
			t.Errorf("allocs=%d frees=%d — more frees than allocs is a double free; a "+
				"single-bind Option stays the consuming-match analysis's", allocs, frees)
		}
	})
}

// TestSelfHostOptSiblingRebindHazardsX86_64 — the shapes the widened credit must
// still REFUSE. A wrongly-granted one frees a chain something else still reads,
// so these assert behaviour: the failure mode is a wrong answer or a crash.
func TestSelfHostOptSiblingRebindHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// The Some-arm payload leaves as a bare ident, so the callee may retain it.
			name: "opttup_payload_escapes_into_call",
			src: `function take(a: i32[]): i32 { return a.len(); }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var k: i32 = 0;
    while (k < 3) { o = Some((k, [k, k + 1])); k = k + 1; }
    match (o) { Some(t) => { acc = take(t.1); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 49,
		},
		{
			// A rebind whose tuple element is a live local's buffer. Freeing it at the
			// next rebind would dangle `shared`, read after the match.
			name: "opttup_rebind_payload_not_fresh",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    var shared: i32[] = [i, i + 1];
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var k: i32 = 0;
    while (k < 3) { o = Some((k, shared)); k = k + 1; }
    match (o) { Some(t) => { acc = t.0 + t.1.len(); }, None => {} }
    return acc + shared[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 67;
}`,
			want: 60,
		},
		{
			// Matched twice — not dead after the first, so releasing there would read
			// a freed box in the second.
			name: "opttup_used_after_match",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var k: i32 = 0;
    while (k < 3) { o = Some((k, [k, k + 1])); k = k + 1; }
    match (o) { Some(t) => { acc = t.1.len(); }, None => {} }
    match (o) { Some(u) => { acc = acc + u.0; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 61;
}`,
			want: 57,
		},
		{
			// The Option leaves by return, so the callee must not free it. There is no
			// consuming match in `build` at all, which is what refuses it.
			name: "opttup_escaping_return",
			src: `function build(i: i32): Option[(i32, i32[])] {
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var k: i32 = 0;
    while (k < 3) { o = Some((k, [k, k + 1])); k = k + 1; }
    return o;
}
function round(i: i32): i32 {
    var r: Option[(i32, i32[])] = build(i);
    match (r) { Some(t) => { return t.0 + t.1.len(); }, None => { return 0; } }
    return 0;
}
function main(): i32 {
    var x: i32 = 0;
    var q: i32 = 0;
    while (q < 60) { x = x + round(q); q = q + 1; }
    return x % 59;
}`,
			want: 4,
		},
		{
			// A rebind from a CALL, not a fresh literal: the slot becomes the only
			// reference to a box the callee produced, and the whole name is refused.
			name: "opttup_rebind_from_a_call",
			src: `function mk(k: i32): Option[(i32, i32[])] { return Some((k, [k, k + 1])); }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var k: i32 = 0;
    while (k < 3) { o = mk(k); k = k + 1; }
    match (o) { Some(t) => { acc = t.0 + t.1.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 53;
}`,
			want: 28,
		},
		{
			name: "optstruct_payload_escapes_into_call",
			src: `struct P { xs: i32[], n: i32 }
function take(p: P): i32 { return p.n; }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    var k: i32 = 0;
    while (k < 3) { o = Some(P { xs: [k, k + 1], n: k }); k = k + 1; }
    match (o) { Some(p) => { acc = take(p); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 49,
		},
		{
			name: "optstruct_escaping_return",
			src: `struct P { xs: i32[], n: i32 }
function build(i: i32): Option[P] {
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    var k: i32 = 0;
    while (k < 3) { o = Some(P { xs: [k, k + 1], n: k }); k = k + 1; }
    return o;
}
function round(i: i32): i32 {
    var r: Option[P] = build(i);
    match (r) { Some(p) => { return p.n + p.xs.len(); }, None => { return 0; } }
    return 0;
}
function main(): i32 {
    var x: i32 = 0;
    var q: i32 = 0;
    while (q < 60) { x = x + round(q); q = q + 1; }
    return x % 59;
}`,
			want: 4,
		},
		{
			// The struct-literal REBIND stores a live local's buffer into the field.
			// Unlike the tuple sibling this one IS admitted — a bare-ident array field
			// value takes the struct-literal construction retain, so the deep drop is
			// balanced against it and `shared` survives to be read at the end. The
			// exit code is what proves that rather than the leak figure.
			name: "optstruct_rebind_field_aliases_a_local",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var shared: i32[] = [i, i + 1];
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    var k: i32 = 0;
    while (k < 3) { o = Some(P { xs: shared, n: k }); k = k + 1; }
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    return acc + shared[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 67;
}`,
			want: 60,
		},
		{
			name: "optarrarr_rebind_payload_not_fresh",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    var shared: i32[][] = [[i, i + 1], [i + 2]];
    var o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    var k: i32 = 0;
    while (k < 3) { o = Some(shared); k = k + 1; }
    match (o) { Some(g) => { acc = g.len(); }, None => {} }
    return acc + shared.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 67;
}`,
			want: 39,
		},
		{
			name: "optarrarr_used_after_match",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    var k: i32 = 0;
    while (k < 3) { o = Some([[k, k + 1], [k + 2]]); k = k + 1; }
    match (o) { Some(g) => { acc = g.len(); }, None => {} }
    match (o) { Some(h) => { acc = acc + h.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 61;
}`,
			want: 57,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "optsib_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
