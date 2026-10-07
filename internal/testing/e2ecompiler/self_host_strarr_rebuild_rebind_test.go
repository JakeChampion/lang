package e2ecompiler

import (
	"testing"
)

// --- A string[] local REBUILT by a rebind ------------------------------------
//
// `let x: string[] = [mk("x")]; x = [mk("y"), mk("z")];` must balance: the
// rebind releases the old array's buffer AND its element strings (#5338). It is
// `str_arr__rebind__{read,unused}` on both leak-matrix arches.
//
// The failure mode to guard is an over-release — freeing element boxes another
// holder could still reach — so the hazard rows are essential. Five shapes
// return the array after rebuilding it, bind an element out of it, rebind from
// a live local, rebuild from the array's own element, or store it into a
// container; each reads its value back after 200 rounds of churn have recycled
// the freelist and answers as `bin/fern -interp` does. Every row balances.
//
// `alias_before_rebind` binds an alias BEFORE the rebind and still goes CLEAN:
// __fern_str_arr_free walks elements only at the last owner's rc 1. It reads
// every element back after churn.

const strarrRebuildDecl = `function mkstr(a: string): string { return a + "-long-enough-to-heap-allocate"; }
function mkarr(i: i32): string[] { let o: string[] = []; o = o.append(mkstr("a")); o = o.append(mkstr("b")); return o; }
function churn(i: i32): i32 { let a: string[] = mkarr(i); let b: string[] = mkarr(i + 1); return a[0].len() + b[1].len(); }
`

const strarrRebuildChurnMain = `
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

const strarrRebuildPlainMain = `
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func strarrRebuildCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// THE CELL, in the matrix's own spelling. 800/200 before.
			name: "literal_rebuild",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string[] = [mkstr("x")];
    x = [mkstr("y"), mkstr("z")];
    t = (t + x.len()) % 101;
    t = t + 1;
    return t;
}` + strarrRebuildPlainMain,
			want: 51,
		},
		{
			// The producer-call form of the same rebuild, which the declaration
			// already admitted through the whole-program "STRARR:" registry.
			name: "producer_rebuild",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string[] = mkarr(i);
    x = mkarr(i + 1);
    t = (t + x.len()) % 101;
    return t + 1;
}` + strarrRebuildPlainMain,
			want: 51,
		},
		{
			// Rebuilding to `[]` drops every element the slot held — the case
			// that isolated the shallow store, since the new value has no
			// elements of its own to mask a missed free.
			name: "rebuild_to_empty",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string[] = [mkstr("x")];
    x = [];
    t = (t + x.len()) % 101;
    return t + 1;
}` + strarrRebuildPlainMain,
			want: 17,
		},
		{
			// Three superseded buffers per round, each freed at its own store.
			name: "loop_rebuild",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let x: string[] = [mkstr("x")];
    let j: i32 = 0;
    while (j < 3) { x = [mkstr("y"), mkstr("z")]; j = j + 1; }
    let junk: i32 = churn(i);
    if (x.len() != 2) { return 0 - 1; }
    return (x[0].len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 72,
		},
		{
			// Half the rounds take the rebind; the other half leave the
			// declaration's value to the exit sweep.
			name: "conditional_rebuild",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let x: string[] = [mkstr("x")];
    if (i % 2 == 0) { x = [mkstr("y"), mkstr("z")]; }
    let junk: i32 = churn(i);
    if (x.len() < 1) { return 0 - 1; }
    return (x[0].len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 72,
		},
		{
			// NOT a refusal — the arbitration. The alias is bound before the
			// rebind and read after churn; it earns the same "SARR:" credit, so
			// the store's free finds rc 2 and only decs, and the element walk
			// runs once at whichever owner reaches rc 1.
			name: "alias_before_rebind",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let want: i32 = mkstr("x").len();
    let x: string[] = [mkstr("x")];
    let ys: string[] = x;
    x = [mkstr("y"), mkstr("z")];
    let junk: i32 = churn(i);
    if (ys[0].len() != want) { return 0 - 1; }
    return (ys.len() + x.len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 67,
		},
		{
			// Control: the self-append rebind, sanctioned and clean before this
			// change. If it moves, the rebuild admission reached into it.
			name: "self_append_unchanged",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string[] = [mkstr("x")];
    x = x.append(mkstr("y"));
    t = (t + x.len()) % 101;
    return t + 1;
}` + strarrRebuildPlainMain,
			want: 51,
		},
		{
			// A local returned bare after its rebuild (#10721): the rebind releases
			// the superseded array and the return moves the rebuilt one out.
			name: "returned_after_rebuild",
			src: strarrRebuildDecl + `function grab(i: i32): string[] { let x: string[] = [mkstr("x")]; x = [mkstr("y"), mkstr("z")]; return x; }
function round(i: i32): i32 {
    let want: i32 = mkstr("y").len();
    let xs: string[] = grab(i);
    let junk: i32 = churn(i);
    if (xs.len() != 2) { return 0 - 1; }
    if (xs[0].len() != want) { return 0 - 2; }
    return (xs[1].len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 72,
		},
		{
			// An ELEMENT is bound out before the rebind, so the store's
			// element walk would free a box the local still reads.
			name: "refused_element_bound",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let want: i32 = mkstr("x").len();
    let x: string[] = [mkstr("x")];
    let e: string = x[0];
    x = [mkstr("y"), mkstr("z")];
    let junk: i32 = churn(i);
    if (e.len() != want) { return 0 - 1; }
    return (e.len() + x.len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 57,
		},
		{
			// The rebind value is another LIVE local, not a rebuild.
			name: "refused_nonfresh_rebind",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let other: string[] = [mkstr("o")];
    let x: string[] = [mkstr("x")];
    x = other;
    let junk: i32 = churn(i);
    if (x.len() != other.len()) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 82,
		},
		{
			// The new literal is built FROM the array's own element, so
			// the value shares a box with what the store is about to free.
			name: "refused_self_element",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let x: string[] = [mkstr("x"), mkstr("q")];
    x = [x[0]];
    let junk: i32 = churn(i);
    if (x.len() != 1) { return 0 - 1; }
    if (x[0].len() < 10) { return 0 - 2; }
    return (x[0].len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 72,
		},
		{
			// The rebuilt value is stored into a container that
			// outlives the sweep.
			name: "refused_container_store",
			src: strarrRebuildDecl + `function round(i: i32): i32 {
    let x: string[] = [mkstr("x")];
    x = [mkstr("y"), mkstr("z")];
    let boxes: string[][] = [x];
    let junk: i32 = churn(i);
    if (boxes[0].len() != x.len()) { return 0 - 1; }
    return (boxes[0].len() + junk) % 101;
}` + strarrRebuildChurnMain,
			want: 33,
		},
	}
}

// TestSelfHostStrArrRebuildRebindX86_64 — a string[] local rebuilt by a rebind
// releases the buffer AND its element boxes at the store, and every use that
// could still reach one of them refuses the credit.
func TestSelfHostStrArrRebuildRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strarrRebuildCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrrebuild_"+tc.name, asm)
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
