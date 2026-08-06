package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Rebound Option[<flat scalar array>] reclaim (#6127) ---------------------
//
// The Option loop-rebind family had four classes — OPTAARR (Option[<arr>][]),
// OPTARRARR (Option[i32[][]]), OPTSTRUCT, OPTTUP — and none for the simplest
// payload of all, a flat scalar array.
//
// A SINGLE-BIND Option[i32[]] never needed one: consumed_rcpayload_option_frees
// frees it at its consuming match, at fn level and (since #4357) per block. What
// leaked was the REBOUND form, because every one of those consuming-match
// analyses refuses a reassigned name outright and no rebind class existed to pick
// it up. A gate matrix isolated it — same Option, same consuming match, differing
// only in whether the local is rebound:
//
//	top-level, single bind             0   2 releases
//	nested in a while, per-iteration    0   2 releases
//	REBOUND in a loop              40000   0 releases
//
// The new OPTARR: class credits ONLY names that ARE reassigned, which is the
// exact complement of those analyses' gate — so a name is claimed by one side or
// the other and never both. That disjointness is what the mixed test below pins.
//
// These assert allocs == frees alongside live_bytes == 0, because the two
// directions mean different things: frees > allocs is a double free (both
// analyses claimed one box), frees < allocs is an unclaimed box.

// TestSelfHostOptArrRebindReclaimX86_64 — a rebound Option[i32[]] reclaims every
// superseded payload buffer and option box, and the single-bind forms that were
// already correct stay correct.
func TestSelfHostOptArrRebindReclaimX86_64(t *testing.T) {
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

	t.Run("rebound_in_loop", func(t *testing.T) {
		src := `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[]] = Some([i, i + 1]);
    var k: i32 = 0;
    while (k < 4) { o = Some([k, k + 1]); k = k + 1; }
    match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
		allocs, frees, live := counts(t, "optarr_rebound", src, 2)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — every superseded payload buffer and option "+
				"box must be freed at the rebind; the leak scales with the iteration "+
				"count, so any nonzero here is unbounded", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
		}
	})

	t.Run("single_bind_top_level_unchanged", func(t *testing.T) {
		// Owned by consumed_rcpayload_option_frees, NOT by this class. If OPTARR:
		// also claimed it, its box would be dec'd twice.
		src := `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`
		allocs, frees, live := counts(t, "optarr_single_top", src, 9)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — a single-bind top-level Option is the "+
				"consuming-match analysis's, and must stay balanced", allocs, frees, live)
		}
	})

	t.Run("single_bind_nested_block_unchanged", func(t *testing.T) {
		// Owned by the per-block call added in #4357 — same disjointness check one
		// scope deeper.
		src := `function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 4) {
        var o: Option[i32[]] = Some([k, k + 1]);
        match (o) { Some(a) => { acc = acc + a.len() + a[0]; }, None => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 89;
}`
		allocs, frees, live := counts(t, "optarr_single_nested", src, 65)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — a nested single-bind Option is the "+
				"block-level analysis's, and must stay balanced", allocs, frees, live)
		}
	})
}

// TestSelfHostOptArrRebindHazardsX86_64 — the shapes the class must still REFUSE.
// A wrongly-granted credit frees a buffer something else still reads, so these
// assert behaviour: the failure mode is a wrong answer or a crash, not a leak.
func TestSelfHostOptArrRebindHazardsX86_64(t *testing.T) {
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
			// The Some-arm payload leaves as a bare ident, so the callee may retain
			// the buffer. Note `a[0]` and `a.len()` are BORROWS and stay admissible —
			// distinguishing those from this is why the arr-of-arr payload walker
			// could not be reused (there `g[i]` extracts a row pointer).
			name: "payload_escapes_into_call",
			src: `function take(a: i32[]): i32 { return a.len(); }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[]] = Some([i, i + 1]);
    var k: i32 = 0;
    while (k < 3) { o = Some([k, k + 1]); k = k + 1; }
    match (o) { Some(a) => { acc = take(a); }, None => {} }
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
			// A rebind whose payload is a live local's buffer, not a fresh literal.
			// Freeing it at the next rebind would dangle `shared`, which is read
			// after the match.
			name: "rebind_payload_not_fresh",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    var shared: i32[] = [i, i + 1];
    var o: Option[i32[]] = Some([i, i + 1]);
    var k: i32 = 0;
    while (k < 3) { o = Some(shared); k = k + 1; }
    match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    return acc + shared[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 67;
}`,
			want: 35,
		},
		{
			// Matched twice — not dead after the first, so freeing there would read a
			// released box in the second.
			name: "used_after_match",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32[]] = Some([i, i + 1]);
    var k: i32 = 0;
    while (k < 3) { o = Some([k, k + 1]); k = k + 1; }
    match (o) { Some(a) => { acc = a.len(); }, None => {} }
    match (o) { Some(b) => { acc = acc + b[0]; }, None => {} }
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
			// The option itself escapes by return, so the callee must not free it.
			name: "escaping_return",
			src: `function build(i: i32): Option[i32[]] {
    var o: Option[i32[]] = Some([i, i + 1]);
    var k: i32 = 0;
    while (k < 3) { o = Some([k, k + 1]); k = k + 1; }
    return o;
}
function round(i: i32): i32 {
    var r: Option[i32[]] = build(i);
    match (r) { Some(a) => { return a.len() + a[0]; }, None => { return 0; } }
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "optarr_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
