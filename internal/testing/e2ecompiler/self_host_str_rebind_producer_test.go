package e2ecompiler

import (
	"testing"
)

// --- A string local REBOUND from a producer call ------------------------------
//
// `let x: string = mk("x"); x = mk("yz");` frees the superseded box at the
// reassignment and the final one at scope exit, exactly as a rebound concat
// (`let x = "a" + b; x = "c" + d;`) does: `mk` returns a fresh box. It is the
// `str__rebind__{read,unused}` pair of the leak matrix (#5338).
//
// Three shapes could over-release: an alias bound before the rebind (which
// would point at a box the rebind frees), a rebind from a non-fresh value
// (which would alias a live box), and a store into a container. Each reads its
// value back after 200 rounds of churn have recycled the freelist, and each
// answers identically on `bin/fern -interp` and the self-host. Every row
// balances.

const strRebindDecl = `function mkstr(a: string): string { return a + "-long-enough-to-heap-allocate"; }
function churn(i: i32): i32 { let a: string = mkstr("c"); let b: string = mkstr("d"); return a.len() + b.len(); }
`

// strRebindChurnMain drives 200 rounds and separates the failure modes: a
// negative round result (a value read back wrong, i.e. an over-release) exits
// 100, a non-zero underflow counter exits 99, and reading freed memory
// segfaults on its own.
const strRebindChurnMain = `
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
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
    let t: i32 = 0;
    let x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    t = t + 1;
    return t;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 68,
		},
		{
			// The same shape with strings too long for SSO, so native allocates
			// too and the two compilers can be compared on the accounting
			// rather than only on the exit.
			name: "producer_rebind_heap",
			src: strRebindDecl + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    return t + 1;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 46,
		},
		{
			// A rebind inside an `if`, so half the rounds take it and half
			// leave the declaration's box to the exit sweep.
			name: "conditional_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    if (i % 2 == 0) { x = mkstr("yz"); }
    let junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 6,
		},
		{
			// A loop rebind: three superseded boxes per round, each freed at
			// its own store rather than accumulating.
			name: "loop_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    let j: i32 = 0;
    while (j < 3) { x = mkstr("y"); j = j + 1; }
    let junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 72,
		},
		{
			// The rebind CONSUMES the local (`x = mk(x)`), which is the
			// accumulator's own shape reached through a producer rather than a
			// concat. Native leaks this one; the self-host does not.
			name: "self_consuming_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    x = mkstr(x);
    let junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 31,
		},
		{
			// The final value is MOVED OUT by a bare `return x`, which the
			// class already treats as safe. The superseded box is still freed
			// at the rebind; the returned one is the caller's.
			name: "moved_out_return",
			src: strRebindDecl + `function grab(i: i32): string { let x: string = mkstr("x"); x = mkstr("yz"); return x; }
function round(i: i32): i32 {
    let want: i32 = mkstr("yz").len();
    let s: string = grab(i);
    let junk: i32 = churn(i);
    if (s.len() != want) { return 0 - 1; }
    return (s.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 23,
		},
		{
			// Control: the single-bind sibling, clean before this change. If it
			// moves, the widening reached past the rebind it is scoped to.
			name: "single_bind_unchanged",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string = mkstr("x");
    t = (t + x.len()) % 101;
    return t + 1;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 51,
		},
		{
			// An alias is bound BEFORE the rebind, so crediting would
			// free a box `y` still points at. This is the hazard the class's
			// own comment names, and it is why the fix is the registry rather
			// than a loosened gate.
			name: "refused_alias_before_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let want: i32 = mkstr("x").len();
    let x: string = mkstr("x");
    let y: string = x;
    x = mkstr("yz");
    let junk: i32 = churn(i);
    if (y.len() != want) { return 0 - 1; }
    return (y.len() + x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 16,
		},
		{
			// The rebind value is another LIVE local, not a fresh box,
			// so the slot would hold an alias at exit.
			name: "refused_nonfresh_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let other: string = mkstr("o");
    let x: string = mkstr("x");
    x = other;
    let junk: i32 = churn(i);
    if (x.len() != other.len()) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 72,
		},
		{
			// The final value is stored into a container, which
			// outlives the sweep.
			name: "refused_container_store",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    x = mkstr("yz");
    let box: string[] = [x];
    let junk: i32 = churn(i);
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
