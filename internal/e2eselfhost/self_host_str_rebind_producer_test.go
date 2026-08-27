package e2eselfhost

import (
	"testing"
)

// --- A string local REBOUND from a producer call ------------------------------
//
// `var x: string = mk("x"); x = mk("yz");` measured 400 allocs / 0 frees on the
// self-host — not a partial sweep, nothing freed at all — and it is the
// `str__rebind__{read,unused}` pair of the leak matrix (#5338).
//
// THE REFUSAL WAS NARROWER THAN THE NOTE SAID. The matrix recorded it as "the
// STR: credit is single-bind only", but the string-builder accumulator class
// has released a rebound local since #2649: it frees the superseded box at each
// reassignment and the final one at scope exit, and str_accum_reassign_ok's
// default arm already admits a rebind RHS that does not mention the local at
// all. `var x = "a" + b; x = "c" + d;` was clean before this change.
//
// What it could not see was that `mk` returns a fresh box. That proof is
// whole-program — str_fresh_ret_fns_of's registry — and the accumulator
// collector answered from the expression alone. So a producer-call rebind fell
// between two collectors: this one could not tell mk was fresh, and
// collect_str_fresh_ret_call_names, which can, skips every reassigned name by
// construction. Neither credited it, so neither box was ever swept.
//
// The fix is the registry, threaded into the class that already had the
// machinery: str_accum_value_is_fresh is the expression test OR the registry
// lookup, and both the declaration and the rebind ask it.
//
// WHAT DID NOT MOVE IS THE POINT. The class's own gates are untouched, so the
// three shapes that must stay refused still are, each pinned below at its
// leaking count: an alias bound before the rebind (which would point at a box
// the rebind frees), a rebind from a non-fresh value (which would alias a live
// box), and a store into a container. Each reads its value back after 200
// rounds of churn have recycled the freelist, and each answers identically on
// native x86-64, `bin/fern -interp` and the self-host.
//
// Every flipped row was re-run under FERN_SANITIZE=1 with
// FERN_RC_UNDERFLOW_TRAP=1 and FERN_RC_FREE_DEBUG=1: clean, no trap, no
// quarantine hit.

const strRebindDecl = `function mkstr(a: string): string { return a + "-long-enough-to-heap-allocate"; }
function churn(i: i32): i32 { var a: string = mkstr("c"); var b: string = mkstr("d"); return a.len() + b.len(); }
`

// strRebindChurnMain drives 200 rounds and separates the failure modes: a
// negative round result (a value read back wrong, i.e. an over-release) exits
// 100, a non-zero underflow counter exits 99, and reading freed memory
// segfaults on its own.
const strRebindChurnMain = `
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func strRebindCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// THE CELL, in the matrix's own spelling. 400/0 before, 400/400 now.
			name: "producer_rebind_read",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: i32 = 0;
    var x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    t = t + 1;
    return t;
}
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 68, balance: true,
		},
		{
			// The same shape with strings too long for SSO, so native allocates
			// too and the two compilers can be compared on the accounting
			// rather than only on the exit.
			name: "producer_rebind_heap",
			src: strRebindDecl + `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    return t + 1;
}
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 46, balance: true,
		},
		{
			// A rebind inside an `if`, so half the rounds take it and half
			// leave the declaration's box to the exit sweep.
			name: "conditional_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    var x: string = mkstr("x");
    if (i % 2 == 0) { x = mkstr("yz"); }
    var junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 6, balance: true,
		},
		{
			// A loop rebind: three superseded boxes per round, each freed at
			// its own store rather than accumulating.
			name: "loop_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    var x: string = mkstr("x");
    var j: i32 = 0;
    while (j < 3) { x = mkstr("y"); j = j + 1; }
    var junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 72, balance: true,
		},
		{
			// The rebind CONSUMES the local (`x = mk(x)`), which is the
			// accumulator's own shape reached through a producer rather than a
			// concat. Native leaks this one; the self-host does not.
			name: "self_consuming_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    var x: string = mkstr("x");
    x = mkstr(x);
    var junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 31, balance: true,
		},
		{
			// The final value is MOVED OUT by a bare `return x`, which the
			// class already treats as safe. The superseded box is still freed
			// at the rebind; the returned one is the caller's.
			name: "moved_out_return",
			src: strRebindDecl + `function grab(i: i32): string { var x: string = mkstr("x"); x = mkstr("yz"); return x; }
function round(i: i32): i32 {
    var want: i32 = mkstr("yz").len();
    var s: string = grab(i);
    var junk: i32 = churn(i);
    if (s.len() != want) { return 0 - 1; }
    return (s.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 23, balance: true,
		},
		{
			// Control: the single-bind sibling, clean before this change. If it
			// moves, the widening reached past the rebind it is scoped to.
			name: "single_bind_unchanged",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: i32 = 0;
    var x: string = mkstr("x");
    t = (t + x.len()) % 101;
    return t + 1;
}
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 51, balance: true,
		},
		{
			// REFUSED: an alias is bound BEFORE the rebind, so crediting would
			// free a box `y` still points at. This is the hazard the class's
			// own comment names, and it is why the fix is the registry rather
			// than a loosened gate.
			name: "refused_alias_before_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    var want: i32 = mkstr("x").len();
    var x: string = mkstr("x");
    var y: string = x;
    x = mkstr("yz");
    var junk: i32 = churn(i);
    if (y.len() != want) { return 0 - 1; }
    return (y.len() + x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 16,
		},
		{
			// REFUSED: the rebind value is another LIVE local, not a fresh box,
			// so the slot would hold an alias at exit.
			name: "refused_nonfresh_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    var other: string = mkstr("o");
    var x: string = mkstr("x");
    x = other;
    var junk: i32 = churn(i);
    if (x.len() != other.len()) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 72,
		},
		{
			// REFUSED: the final value is stored into a container, which
			// outlives the sweep.
			name: "refused_container_store",
			src: strRebindDecl + `function round(i: i32): i32 {
    var x: string = mkstr("x");
    x = mkstr("yz");
    var box: string[] = [x];
    var junk: i32 = churn(i);
    if (box[0].len() != x.len()) { return 0 - 1; }
    return (box[0].len() + junk) % 101;
}` + strRebindChurnMain,
			want: 23,
		},
	}
}

// TestSelfHostStrRebindProducerX86_64 — a string local rebound from a
// whole-program-proven fresh producer is the accumulator class's own shape, and
// the gates that keep an alias or a non-fresh rebind out still do.
func TestSelfHostStrRebindProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strrebind_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = a value read back "+
					"wrong, i.e. an over-release; 139 = it read freed memory)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
			if !tc.balance && live == 0 && allocs == frees {
				t.Errorf("%s: %s — pinned as REFUSED; if this now balances the credit "+
					"widened, and the row belongs to whatever widened it", tc.name, summary)
			}
		})
	}
}
