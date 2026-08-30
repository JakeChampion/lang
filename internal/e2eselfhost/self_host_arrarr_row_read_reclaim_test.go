package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Index row reads no longer forfeit the arr-of-arr credit (#7805) ---------
//
// irlower's "ARRARR:" credit routes a fresh, non-escaping arr-of-arr local to
// the deep release (__fern_arrarr_free), which rc-decs each row buffer and then
// frees the outer one. arrarr_row_escapes used to refuse that credit for ANY
// bare single-index row read — `var row = g[i]` or `row = g[i]` — on the
// grounds that the bound row would dangle when the reclaim ran.
//
// It would not: both index spellings take a Perceus dup at the bind (the
// is_arr/ExprIndex retain in lower_stmt_var and its assign twin), so the row is
// a counted reference and the walk's rc-guarded dec lands on 2, not 1. Refusing
// the credit dropped the slot to the generic is_arr sweep, which decs only the
// OUTER buffer — so every row stranded, including rows nothing else touched,
// at 88 B/round against native's zero.
//
// `for row in g` genuinely takes no dup (measured: zero rc_inc for the
// iteration form against one for each index form), so it stays an escape and
// keeps the shallow release. TestSelfHostArrArrRowReadHazardsX86_64 pins that
// the shapes still refused stay CORRECT, which is the half a wrongly-granted
// credit would break: an over-release is a use-after-free, not a leak.
//
// Differential against native, as the rest of this package is: the assertion is
// AGREEMENT with the native compiler, not an absolute byte count.

// arrarrRowBindChurnSrc: the minimal shape — a row bound out of a fresh
// arr-of-arr and read locally. Nothing escapes the frame.
const arrarrRowBindChurnSrc = `function round(n: i32): i32 {
    var placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    var row: i32[] = placed[0];
    return row[0] + placed[1][8];
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

// arrarrRowAssignChurnSrc: the ASSIGN spelling of the same read, which takes
// the same dup and so earns the same credit.
const arrarrRowAssignChurnSrc = `function round(n: i32): i32 {
    var placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    var row: i32[] = [0];
    row = placed[0];
    return row[0] + placed[1][8];
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

// arrarrRowEscapeChurnSrc: the row ESCAPES in a returned struct's scalar-array
// field — the #7805 repro. The construction adds a second retain and the
// struct's own field drop balances it, so the walk still lands correctly.
const arrarrRowEscapeChurnSrc = `struct G { text: string, lines: i32[] }

function mk(n: i32): G {
    var placed: i32[][] = [[n], [n, n, n, n, n, n, n, n, n]];
    var row: i32[] = placed[0];
    return G { text: "a", lines: row };
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { var g: G = mk(r); t = t + g.lines[0] + g.lines.len(); r = r + 1; }
    return t % 3;
}`

func TestSelfHostArrArrRowReadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"row_bind", arrarrRowBindChurnSrc},
		{"row_assign", arrarrRowAssignChurnSrc},
		{"row_escapes_in_struct_field", arrarrRowEscapeChurnSrc},
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

// arrarrRowIterHazardSrc: `for row in g` takes NO dup, so the credit must still
// be refused and the rows leak soundly. Pinned on BEHAVIOUR, not bytes — if the
// credit were wrongly granted here the deep free would dangle a row the loop
// still reads, which shows up as a wrong answer or a crash rather than as a
// leak. Rows are distinct so a freed-and-recycled buffer changes the sum.
const arrarrRowIterHazardSrc = `function round(n: i32): i32 {
    var placed: i32[][] = [[n, n + 1], [n + 2, n + 3], [n + 4, n + 5]];
    var t: i32 = 0;
    for row in placed { t = t + row[0] + row[1]; }
    return t;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

// arrarrRowRebindHazardSrc: the reclaim-at-rebind shape. A self-append rebind
// releases the previous structure mid-loop while `held` still names a row bound
// in an EARLIER iteration. The bind's dup is what makes this safe — the reclaim
// decs that row but cannot free it — so a correct answer here is the evidence
// that granting the credit did not introduce an over-release.
const arrarrRowRebindHazardSrc = `function round(n: i32): i32 {
    var g: i32[][] = [];
    var held: i32[] = [0];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 5) {
        g = g.append([i + n, i + n + 1, i + n + 2]);
        held = g[0];
        t = t + held[0] + g[i][1];
        i = i + 1;
    }
    return t + held[2];
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

func TestSelfHostArrArrRowReadHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"for_in_iteration", arrarrRowIterHazardSrc},
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
