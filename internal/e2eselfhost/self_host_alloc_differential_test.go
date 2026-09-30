package e2eselfhost

import (
	"fmt"
	"os/exec"
	"testing"
)

// Nothing else measures how MUCH the compiler allocates. That is how the
// native and self-host compilers once developed opposite cliffs undetected — a
// shape that leaks megabytes stays green everywhere, because every other gate
// asks "is the answer right?" and this class of bug never changes the answer.
// docs/TEST-GATES.md lists allocation volume under "what nothing gates at
// all"; this is that gate.
//
// It reads two probes and holds each shape to the figures this gate recorded
// for it (the t.Logf line is where a recorded figure comes from; a hand-run
// CLI measures a different pipeline):
//
//   - __heap_bump_bytes()        bytes handed out fresh (what the freelist
//                                could not recycle), per churn
//   - __arr_push_shared_count()  appends that copied a buffer which still had
//                                spare capacity — the rc==1 cliff
//
// WHAT IT DOES NOT ASSERT: byte equality. Box layout, capacity schedules and
// the string representation all move the totals, so an exact-match gate would
// be noise, and noise gets muted. Two comparisons survive that objection:
//
//  1. the cliff counter staying ZERO or NON-ZERO as recorded. It counts
//     events, not bytes, so it is layout-free; how many times a shape crosses
//     the cliff moves with the capacity schedule, whether it crosses at all
//     is the part that means something.
//  2. the per-churn bump delta staying within a RATIO of the recorded figure
//     upward. The regressions this exists to catch were three and four orders
//     of magnitude; a ratio bound catches those while tolerating layout.
//  3. a row that records a LEAK failing once the leak shrinks to less than
//     half the recorded figure (leakShrinkFactor). A fix, whole or partial,
//     has to be re-recorded on the row — an expectation nobody re-records is
//     how the next regression hides behind the last fix. The bound is tighter
//     than the regression side's on purpose: within 8x of 15 KB is 2 KB, and
//     an 87% cut to a known leak is exactly the change that must not land
//     silently.
//
// Until 2026-09-29 the second side of every comparison was the Go x86-64
// backend, and divergences between the two compilers were listed in a
// testdata allowlist; the backends are retiring (docs/NATIVE-CONVERGENCE.md
// §3a), so the recorded figures took the native side's place and the listed
// shapes carry their reason on the case.
//
// x86-64 only. Running the same probes again under qemu-aarch64 costs minutes
// to re-answer a question the x86-64 build already answered.

// allocDiffCase is one shape, measured twice per compiler: once for bump
// growth, once for the cliff counter.
//
// decls must define `churn(n: i32): i32` — one self-contained unit of work
// whose allocations are all dead when it returns. Both metric programs call it
// the same way, so the bump delta between two identical churns is exactly what
// the first churn failed to give back.
type allocDiffCase struct {
	name  string
	decls string
	n     int
	// cliff records whether the shape crosses the rc==1 append cliff at all,
	// and bumpKB its per-churn bump growth in KB; both as this gate measured
	// them on x86-64 when the row was written or last re-recorded.
	cliff  bool
	bumpKB int
	// maxRatio bounds measured / max(recorded, 1) on per-churn KB: the
	// regression side. 1 in the denominator so a row that records 0 KB does
	// not divide by zero — it makes a 0-vs-K growth read as ratio K, which is
	// the right severity for small K and correctly severe for large K. The
	// other direction is leakShrinkFactor's.
	maxRatio int
}

// leakShrinkFactor is the improvement bound on a row that records a leak: a
// measured figure below recorded / leakShrinkFactor fails the row until the
// figure is re-recorded, so a fix cannot land with the row still green.
const leakShrinkFactor = 2

var allocDiffCases = []allocDiffCase{
	{
		// The shape every byte-emitter in the self-host compiler is built
		// from: an accumulator threaded through a borrowed param and handed
		// back. Nothing else holds the buffer.
		name: "append-threaded-through-call",
		decls: `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function churn(n: i32): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = step(a, i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// `.with` through a borrowed param — a functional element update.
		// This is the shape docs/TEST-GATES.md cites as having gone 4688 MB
		// native / 0 MB self-host.
		name: "with-through-borrowed-param",
		decls: `function fill(buf: i32[], n: i32): i32[] {
    var i: i32 = 0;
    while (i < n) { buf = buf.with(i, i * 2); i = i + 1; }
    return buf;
}
function churn(n: i32): i32 {
    var b: i32[] = [];
    var i: i32 = 0;
    while (i < n) { b = b.append(0); i = i + 1; }
    b = fill(b, n);
    return b[n - 1];
}`,
		// n is small because this shape's native leak is QUADRATIC — each
		// `.with` abandons an n-element buffer, n times — so the per-churn
		// figure has to stay inside the byte the exit code can carry. At
		// n=200 it overflowed and the guard reported 252.
		n:        80,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// A fresh array per iteration, dead at the end of the body. The
		// baseline both compilers should reclaim completely; if this one ever
		// diverges, the freelist itself has regressed on one side.
		name: "fresh-array-per-iteration",
		decls: `function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var row: i32[] = [i, i + 1, i + 2];
        s = (s + row[0]) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// A struct carrying an array field, rebuilt each iteration — the
		// container shape, where a missed deep drop shows up on one side as
		// steady growth.
		name: "struct-with-array-field",
		decls: `struct Row { xs: i32[], k: i32 }
function mk(k: i32): Row { return Row { xs: [k, k + 1, k + 2], k: k }; }
function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var r: Row = mk(i);
        s = (s + r.xs[1] + r.k) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// An element built in a loop body and pushed onto an accumulator, with
		// a guard clause ahead of the declaration — the shape every parser and
		// tokeniser is written in. The guard cannot leave the body with the
		// element built and unstored, so the push MOVES it; a compiler that
		// refuses the move on the mere presence of the exit keeps a retain
		// whose escape taint suppresses the matching release, and leaks one
		// element per round (#6869: 72 B/round self-host against 0 native).
		name: "loop-push-behind-guard-clause",
		decls: `struct Val { kind: i32, kids: i32[] }
function churn(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        if (i == 9999) { return 12345; }
        var v = Val { kind: i, kids: [] };
        vals = vals.append(v);
        total = (total + vals.len()) % 251;
    }
    return total;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// A TUPLE carrying an owned pointer, stored into a struct field and
		// rebuilt each round. The reclaim credit for this class is recent
		// (#7702 counted the store, the "TCNT:" tier the call-arg half), and
		// nothing measures how MUCH either compiler allocates for it — a
		// credit that fires but releases only the box would leak the element
		// buffer every round while every leak cell still reads clean.
		name: "tuple-in-struct-field",
		decls: `struct Hold { t: (i32, i32[]), n: i32 }
function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var k: (i32, i32[]) = (i, [i, i + 1]);
        var h: Hold = Hold { t: k, n: i };
        s = (s + h.n + h.t.1[1]) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// An Option carrying an array, ALIASED and then consumed by a match —
		// the shape whose reclaim was refused until #7726, because the alias
		// bind read as an escape. Volume is the half that gate cannot see: the
		// leak cell proves the box comes back, not that the two compilers hand
		// out comparable amounts getting there.
		name: "option-array-alias-match",
		decls: `function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var src: Option[i32[]] = Some([i, i + 1]);
        var x: Option[i32[]] = src;
        match (x) { Some(xs) => { s = (s + xs.len()) % 251; }, None => {} }
        match (src) { Some(ys) => { s = (s + ys[0]) % 251; }, None => {} }
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// An Option carrying an ARRAY, as a struct field. This shape did not
		// COMPILE at all until #7745 — the self-host refused the module the
		// moment such a struct was constructed — so a compile failure, not a
		// regression, is what this row replaced.
		name: "option-array-struct-field",
		decls: `struct H { o: Option[i32[]], n: i32 }
function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var h: H = H { o: Some([i, i + 1]), n: i };
        match (h.o) { Some(xs) => { s = (s + xs.len() + h.n) % 251; }, None => {} }
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		// An rc-payload ENUM rebuilt and consumed each round — the family the
		// Option shapes above are modelled on, and the one whose alias-aware
		// reading (#6606) the Option side only just caught up with. Present so
		// the two families are gated symmetrically on volume, not just on
		// whether the box comes back.
		name: "enum-rc-payload-per-iteration",
		decls: `enum E { Full(i32[]), None }
function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var e: E = E.Full([i, i + 1, i + 2]);
        match (e) { E.Full(xs) => { s = (s + xs.len() + xs[0]) % 251; }, E.None => {} }
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
}

// bumpSrc returns a program whose EXIT CODE is the per-churn bump growth in
// KB. Two identical churns, measured between: whatever the first failed to
// give back is what the second has to allocate fresh.
//
// The metric is reported in the exit code rather than stdout because the self-host
// driver resolves no stdlib, so `.to_string()` is unavailable to it. That caps
// the readable range at one byte, hence the 240 KB guard — a shape that leaks
// past it reports 252 rather than silently wrapping into a plausible number.
func (c allocDiffCase) bumpSrc() string {
	return fmt.Sprintf(`%s
function main(): i32 {
    var w: i32 = churn(%d);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(%d);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (w != x) { return 251; }
    var kb: i32 = (b2 - b1) / 1024;
    if (kb > 240) { return 252; }
    return kb;
}
`, c.decls, c.n, c.n)
}

// cliffSrc returns a program whose exit code is the rc==1 append cliff count.
func (c allocDiffCase) cliffSrc() string {
	return fmt.Sprintf(`%s
function main(): i32 {
    var w: i32 = churn(%d);
    if (w < 0) { return 251; }
    var n: i32 = __arr_push_shared_count();
    if (n > 240) { return 240; }
    return n;
}
`, c.decls, c.n)
}

// allocRatio is the severity measure: how many times the measured per-churn
// figure exceeds the recorded one (0 when it does not).
func allocRatio(recorded, measured int) int {
	if measured <= recorded {
		return 0
	}
	if recorded < 1 {
		recorded = 1
	}
	return measured / recorded
}

// TestSelfHostAllocDifferentialX86_64 is the gate. Each shape is compiled by
// the self-host driver, run, and its allocation behaviour held to the row's
// recorded figures.
func TestSelfHostAllocDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	// selfHostExit compiles src with the self-hosted x86-64 driver, links it,
	// runs it, and returns the exit code.
	selfHostExit := func(t *testing.T, label, src string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(src))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", label)
		}
		bin := buildBin(t, gcc, dir, label, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range allocDiffCases {
		t.Run(tc.name, func(t *testing.T) {
			// --- the cliff counter: zero vs non-zero as recorded ---
			cliff := selfHostExit(t, tc.name+"-cliff", tc.cliffSrc())
			if cliff >= 250 {
				t.Fatalf("cliff probe self-check failed (%d); 251 means the churn returned a negative value", cliff)
			}
			if (cliff != 0) != tc.cliff {
				t.Errorf("rc==1 append cliff: crossed it %d time(s), recorded %v — the compiler now "+
					"copies a buffer it used to mutate in place, or the reverse, which is the O(n) vs "+
					"O(n²) split; re-record `cliff` only if the change is intended", cliff, tc.cliff)
			}

			// --- bump growth: within a ratio of the recorded figure ---
			kb := selfHostExit(t, tc.name+"-bump", tc.bumpSrc())
			if kb == 251 {
				t.Fatal("the two churns returned different values — the probe is not measuring identical work")
			}
			if kb == 252 {
				t.Fatal("per-churn growth exceeded the 240 KB the exit code can carry; lower this case's n")
			}
			ratio := allocRatio(tc.bumpKB, kb)

			// Report the measurement on every case, not just the failing ones:
			// a run that prints only PASS tells a human nothing about whether
			// the figure is drifting toward the bound, and a re-recorded
			// `bumpKB` has to come from HERE rather than from a hand-run CLI,
			// which measures a different pipeline.
			t.Logf("n=%d  bump: %d KB per churn (recorded %d KB, %dx, bound %dx)  cliff: %d (recorded %v)",
				tc.n, kb, tc.bumpKB, ratio, tc.maxRatio, cliff, tc.cliff)
			if ratio > tc.maxRatio {
				t.Errorf("per-churn allocation grew %dx past the recorded figure: %d KB now, %d KB "+
					"recorded (bound %dx). A regression, unless the growth is intended, in which "+
					"case re-record `bumpKB` from the line above", ratio, kb, tc.bumpKB, tc.maxRatio)
			}
			if tc.bumpKB > 0 && kb*leakShrinkFactor < tc.bumpKB {
				t.Errorf("the recorded leak shrank from %d KB to %d KB per churn (below a %dth of the "+
					"figure): a fix has landed, whole or partial. Re-record `bumpKB` from the line "+
					"above, and if the leak is gone, retire the leak note on the row",
					tc.bumpKB, kb, leakShrinkFactor)
			}
		})
	}
}
