package e2ecompiler

import (
	"strings"
	"testing"
)

// --- Index row reads keep the arr-of-arr deep release (#7805) ---------------
//
// A fresh, non-escaping arr-of-arr local is released deeply when it dies
// (__fern_arrarr_free): each row buffer is rc-dec'd, then the outer one freed.
// A bare single-index row read — `let row = g[i]` or `row = g[i]` — takes a
// counted reference at the bind, so it must not cost the local that release:
// an outer-only dec strands every row, 88 B/round.
//
// `for row in g` takes no dup, but it is a TRANSIENT borrow — the loop ends
// before the exit release — so it keeps the deep release unless the body lets
// the loop var escape. TestSelfHostArrArrRowReadHazardsX86_64 pins that the
// escaping shapes stay CORRECT, which is the half a wrongly-granted deep release
// would break: an over-release is a use-after-free, not a leak.
//
// The reclaim cases assert live_bytes 0; the hazard cases assert the self-host
// driver's answer agrees with the `fern` CLI build of the same program.

// arrarrRowBindChurnSrc: the minimal shape — a row bound out of a fresh
// arr-of-arr and read locally. Nothing escapes the frame.
const arrarrRowBindChurnSrc = `function round(n: i32): i32 {
    let placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    let row: i32[] = placed[0];
    return row[0] + placed[1][8];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

// arrarrRowAssignChurnSrc: the ASSIGN spelling of the same read, which takes
// the same dup and so earns the same credit.
const arrarrRowAssignChurnSrc = `function round(n: i32): i32 {
    let placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    let row: i32[] = [0];
    row = placed[0];
    return row[0] + placed[1][8];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

// arrarrRowEscapeChurnSrc: the row ESCAPES in a returned struct's scalar-array
// field — the #7805 repro. The construction adds a second retain and the
// struct's own field drop balances it, so the walk still lands correctly.
const arrarrRowEscapeChurnSrc = `struct G { text: string, lines: i32[] }

function mk(n: i32): G {
    let placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    let row: i32[] = placed[0];
    return G { text: "a", lines: row };
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { let g: G = mk(r); t = t + g.lines[0] + g.lines.len(); r = r + 1; }
    return t % 3;
}`

func TestSelfHostArrArrRowReadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"row_bind", arrarrRowBindChurnSrc},
		{"row_assign", arrarrRowAssignChurnSrc},
		{"row_escapes_in_struct_field", arrarrRowEscapeChurnSrc},
		{"for_in_iteration", arrarrRowIterSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrarrrow_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)

			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			if summary == "" {
				t.Fatalf("no leakcheck summary (exit %d)", exit)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if allocs == 0 {
				t.Fatal("program allocated nothing — the probe is not exercising the path")
			}
			if live != 0 {
				t.Errorf("%s: live_bytes=%d (allocs=%d frees=%d), want 0 — an index row "+
					"read takes a counted retain, so the arr-of-arr credit holds and every "+
					"row is reclaimed; the leak scales with the iteration count, so any "+
					"nonzero here is unbounded in a loop", summary, live, allocs, frees)
			}
		})
	}
}

// arrarrRowIterSrc: `for row in g` takes no dup, but the loop completes before
// the exit reclaim, so the borrow is transient and the credit holds. Rows are
// distinct so a freed-and-recycled buffer would change the sum.
const arrarrRowIterSrc = `function round(n: i32): i32 {
    let placed: i32[][] = [[n, n + 1], [n + 2, n + 3], [n + 4, n + 5]];
    let t: i32 = 0;
    for row in placed { t = t + row[0] + row[1]; }
    return t;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

// arrarrRowIterEscapeHazardSrc: the loop var ESCAPES the body, so the borrow is
// no longer transient and the credit must still be refused. This is the shape
// that separates "admit transient iteration" from "admit all iteration" — the
// row outlives the loop with no counted reference, so a granted credit would
// free a buffer `kept` still names.
const arrarrRowIterEscapeHazardSrc = `function round(n: i32): i32 {
    let placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    let kept: i32[] = [0];
    for row in placed { kept = row; }
    return kept[0];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

// arrarrRowRebindHazardSrc: the reclaim-at-rebind shape. A self-append rebind
// releases the previous structure mid-loop while `held` still names a row bound
// in an EARLIER iteration. The bind's dup is what makes this safe — the reclaim
// decs that row but cannot free it — so a correct answer here is the evidence
// that granting the credit did not introduce an over-release.
const arrarrRowRebindHazardSrc = `function round(n: i32): i32 {
    let g: i32[][] = [];
    let held: i32[] = [0];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 5) {
        g = g.append([i + n, i + n + 1, i + n + 2]);
        held = g[0];
        t = t + held[0] + g[i][1];
        i = i + 1;
    }
    return t + held[2];
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

func TestSelfHostArrArrRowReadHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"for_in_loop_var_escapes", arrarrRowIterEscapeHazardSrc},
		{"rebind_holds_earlier_row", arrarrRowRebindHazardSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both compilers must agree on the ANSWER. A credit granted to a shape
			// that takes no counted retain frees a row its holder still reads, which
			// shows up as a diverging exit code or a crash long before any byte
			// count would say so. Deliberately NOT a live_bytes assertion: these
			// shapes are expected to leak soundly.
			_, nativeExit := nativeLeakVerdict(t, cli, dir, "arrarrrowhz_nat_"+tc.name, tc.src)
			if nativeExit < 0 {
				t.Fatalf("native side did not run (exit %d)", nativeExit)
			}

			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "arrarrrowhz_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != nativeExit {
				t.Errorf("self-host exited %d, native %d — an arr-of-arr credit granted to a "+
					"shape that takes no counted retain frees a row its holder still reads",
					exit, nativeExit)
			}
		})
	}
}
