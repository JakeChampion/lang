package e2eselfhost

import (
	"os/exec"
	"testing"
)

// The self-host port of native's loop-body-var drop suite
// (internal/e2e/rc_loop_var_test.go), which #4365 records as having no
// equivalent on this side. A `var` re-declared inside a loop reuses ONE slot
// across iterations; without a dec-on-reinit the prior iteration's value is
// overwritten with no release, so N-1 allocations are stranded — unbounded
// growth in a hot build-and-discard loop.
//
// WHAT THIS ASSERTS THAT THE NATIVE SUITE DOES NOT. Native pins the exit code
// plus `__rc_underflow_count()`, and both are already correct on the self-host
// for all four shapes — a compiler that reclaims NOTHING satisfies them
// perfectly, because under-release moves neither. So the port cannot be a
// transcription: each case runs the loop TWICE and compares the bump mark
// across the second run, which is the property that actually distinguishes
// reclaim from silence. Native's own header says the byte property is pinned
// separately by the free-on/free-off differential; this is the self-host's
// equivalent, folded into the program so it needs no second build.
//
// The shape is native's rc_heap_bump_* style: run once to warm the freelist,
// read the mark, run the identical loop again, read again. A reclaiming
// compiler hands the second run the first run's buffers and the mark does not
// move AT ALL — the assertion is exact equality, not a bound, because every
// case measured 0 rather than a small constant on both compilers. The
// per-iteration arithmetic sums to zero, so a buffer handed out while still
// live drifts `acc` and shows up as exit 1 / 2 instead.
var loopVarReclaimCases = []struct {
	name string
	decl string
	body string
	// want is 7 when the bump mark is flat across the second run, 3 when it
	// grows. A 3 is a PINNED DIVERGENCE from native, not an expectation.
	//
	// READ THE MARK FOR WHAT IT IS. It measures whether the allocator hands
	// the second run the first run's buffers — so it grows for a leak AND for
	// a shape that is freed but whose blocks are not reused. Those are
	// different bugs and this probe cannot tell them apart; only FERN_LEAKCHECK
	// (allocs/frees/live_bytes, a compile-time instrumentation with no
	// in-language accessor) can. A row moving 3 -> 7 is unambiguous progress,
	// but a row SITTING at 3 is not evidence the value still leaks, and naming
	// one "-still-leaks" would assert more than the instrument supports.
	want int
}{
	{
		name: "array",
		body: `        var row: i32[] = [i, i * 2, i * 3];
        acc = acc + (row[0] + row[1] + row[2]) - (i * 6);`,
		want: 7,
	},
	{
		name: "struct",
		decl: `struct Pt { x: i32, y: i32 }`,
		body: `        var p: Pt = Pt { x: i, y: i + 1 };
        acc = acc + (p.y - p.x) - 1;`,
		want: 7,
	},
	// #6606's enum half, CLOSED in two steps, and this row is what proved the
	// second one was outstanding.
	//
	// #6622 made a `match (param)` scrutinee a borrow, so the loop-local is
	// admitted and released — 400 allocs / 0 frees became 400 / 396. That killed
	// the 2.00x-per-doubling leak but left this row at 3, and #6626 read the
	// remainder correctly as the mark measuring REUSE rather than frees. Both
	// halves of that reading were right: the boxes really were nearly all freed,
	// and the mark really does grow for a freed-but-not-reused shape. What tied
	// them together is that the 4 stranded boxes (each `work()` call's FINAL
	// value, 2 per call) are exactly what kept the second run from reusing the
	// first run's blocks.
	//
	// The "RCENUMS:" scope-exit sweep credit releases that tail: 400/400,
	// live_bytes 0, and the mark goes flat. So this is the unambiguous 3 -> 7 the
	// `want` note above describes, not a re-reading of a row that stayed put.
	//
	// Renamed a second time, and the churn is the point: "-still-grows" was read
	// as "still leaks" (by me, on #6606), then "-freed-not-reused" named the
	// intermediate state, and now neither holds.
	{
		name: "enum-payload-reclaimed",
		decl: `enum Box { Val(i32[]), Empty }
function head(b: Box): i32 { match (b) { Val(xs) => { return xs[0]; }, Empty => { return 0; } } }`,
		body: `        var b: Box = Val([i, i + 7]);
        acc = acc + head(b) - i;`,
		want: 7,
	},
	// #6606's string half. PARTLY closed by #6624 — the `var` binding earned its
	// credit while the two INLINE suffix(i) calls stayed anonymous temps — and
	// CLOSED by #7292, which keys the "STR:" credit on the binding site so a
	// block-scoped local is swept at all. This row was `-still-leak` / want 3
	// (1000 allocs / 798 frees, 202 boxes stranded and growing with the count);
	// it now reclaims, on both natives.
	//
	// Renamed a THIRD time, and the churn is still the point — see the enum row
	// above, which has its own history of the same thing. A name that asserts a
	// bug persists becomes a lie the moment the bug is fixed, and the test then
	// fails for the right reason while reading as a regression.
	{
		name: "string-concat-temps-reclaimed",
		decl: `function suffix(n: i32): string { if (n % 2 == 0) { return "even"; } return "odd"; }`,
		body: `        var s: string = "v-" + suffix(i);
        acc = acc + s.len() - 2 - suffix(i).len();`,
		want: 7,
	},
}

// loopVarSrc assembles a case into a program that measures itself.
func loopVarSrc(decl, body string) string {
	return decl + `
function work(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
` + body + `
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = work(100);
    var b1: i64 = __heap_bump_bytes();
    var m: i32 = work(100);
    var b2: i64 = __heap_bump_bytes();
    if (w != 0) { return 1; }
    if (m != 0) { return 2; }
    if ((b2 - b1) == (0 as i64)) { return 7; }
    return 3;
}
`
}

// TestSelfHostLoopVarReclaimIRX86_64 runs each case through the self-hosted
// x86-64 IR driver, and cross-checks every case against the NATIVE backend
// first. That cross-check is what makes a `want: 3` row honest: it asserts
// native still answers 7 on the same source, so the row records a self-host
// gap rather than quietly ratifying a shape neither compiler reclaims.
func TestSelfHostLoopVarReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range loopVarReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := loopVarSrc(tc.decl, tc.body)
			if _, code := compileAndRunX86_64(t, src); code != 7 {
				t.Fatalf("native exited %d, want 7 — the shape this case pins is not native's behaviour any more", code)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (7 = reclaimed; 3 = the bump mark grew across an identical second run; 1/2 = the read-back drifted, i.e. a live buffer was handed out)",
					tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostLoopVarReclaimIRArm64 is the arm64 leg. The slot-reinit drop
// lives in shared irlower.fern, so both natives are expected to agree exactly
// — including on the two pinned rows, which measured identical byte counts on
// each. A divergence BETWEEN the legs would mean the gap moved into codegen.
func TestSelfHostLoopVarReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range loopVarReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := loopVarSrc(tc.decl, tc.body)
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (7 = reclaimed; 3 = the bump mark grew; 1/2 = the read-back drifted)",
					tc.name, code, tc.want)
			}
		})
	}
}
