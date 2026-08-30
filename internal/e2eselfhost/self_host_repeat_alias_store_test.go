package e2eselfhost

import (
	"strings"
	"testing"
)

// --- A repeated array alias assignment owes a release (#7814) ----------------
//
// emit_arr_store does two things when a slot is overwritten: retain the new
// reference (alias_inc), and release the old one. The release used to be
// cow-guarded unconditionally — `if (old != new) arr_dec(old)` — so that an
// in-place mutator handing back the SAME buffer did not free the live value.
//
// That guard is right for a self-MUTATION, which creates no second count, and
// wrong for a self-ALIAS, which does. `b = a` executed twice assigns the same
// pointer both times: the second store retained and did not release, stranding
// one buffer. Constant, not per-iteration — each further repeat inflates the
// refcount again but it is the same single block that ends up stranded.
//
// Native states the same rule on its Map twin (internal/ir/ir.go): "a release
// is owed only if an alias inc created a second count for it (`m = m2`); a
// self-mutation created none, and dec'ing there is the over-release ... COW-aware
// branch exists to avoid." So the release is now unconditional exactly when
// alias_inc fired, and cow-guarded otherwise.
//
// Differential against native, like the rest of this package: the assertion is
// AGREEMENT with the native compiler, not an absolute byte count.

// repeatAliasSrc is the minimal shape — no nested array, no append, no element
// read. One execution of `b = a` was always clean; two or more stranded a
// buffer. The loop bound is what the fix turns on, so the churn is the point.
const repeatAliasSrc = `function round(n: i32): i32 {
    var a: i32[] = [n, n, n, n, n];
    var b: i32[] = [7];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { b = a; t = t + b[0]; i = i + 1; }
    return t;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

// repeatAliasRowSrc is the shape that surfaced it: an append-built arr-of-arr
// with a row bound inside the loop. `g[0]` yields the same row pointer every
// iteration, so the bind is a repeated self-alias. This was the last named
// residual of the #7805 / #7810 / #7812 row work.
const repeatAliasRowSrc = `function round(n: i32): i32 {
    var g: i32[][] = [];
    var held: i32[] = [7];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        g = g.append([n, n]);
        held = g[0];
        t = t + held[0] + g[i][1];
        i = i + 1;
    }
    return t + held[1];
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 3;
}`

// repeatSelfMutationSrc is the shape the cow guard exists FOR, kept as the
// control: an in-place append can hand back the same buffer, and that store
// takes no alias inc, so releasing it would free the live value. A fix that
// dropped the guard wholesale instead of conditioning it on alias_inc turns
// this into a use-after-free — which shows up as a wrong answer or a crash,
// so it is asserted on exit-code agreement rather than on bytes.
const repeatSelfMutationSrc = `function round(n: i32): i32 {
    var xs: i32[] = [n];
    var i: i32 = 0;
    while (i < 6) { xs = xs.append(n + i); i = i + 1; }
    return xs[0] + xs[3] + xs.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

func TestSelfHostRepeatAliasStoreX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"repeated_alias", repeatAliasSrc},
		{"repeated_row_alias", repeatAliasRowSrc},
		{"self_mutation_keeps_guard", repeatSelfMutationSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exit codes must agree first: an over-release here frees a buffer
			// the program still reads, which diverges the answer long before
			// any byte count would say so.
			_, nativeExit := nativeLeakVerdict(t, cli, dir, "repalias_nat_"+tc.name, tc.src)
			if nativeExit < 0 {
				t.Fatalf("native side did not run (exit %d)", nativeExit)
			}

			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "repalias_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != nativeExit {
				t.Fatalf("self-host exited %d, native %d — a mismatched release on an "+
					"overwrite frees a buffer the program still holds", exit, nativeExit)
			}

			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			if summary == "" {
				t.Fatal("no leakcheck summary")
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if allocs == 0 {
				t.Fatal("program allocated nothing — the probe is not exercising the path")
			}
			if live != 0 {
				t.Errorf("%s: live_bytes=%d (allocs=%d frees=%d), want 0 — an overwrite "+
					"that retained the new reference owes a release of the old one even "+
					"when they are the same pointer", summary, live, allocs, frees)
			}
		})
	}
}
