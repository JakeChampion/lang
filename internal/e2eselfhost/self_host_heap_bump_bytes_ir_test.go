package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// heapBumpBytesIRCases pin the `__heap_bump_bytes()` introspection builtin — the
// bump allocator's high-water mark (cursor − region base; 0 before the first
// allocation) — on the self-host IR path (#3534). Before this it had no IR
// lowering and bailed the whole module to the legacy AST emitter; it now lowers
// on all three IR backends (x86-64 / arm64 inline cursor−base; wasm `$heap −
// heap_base`).
//
// The interpreter has no bump-allocator model (it always returns 0), so it
// cannot be the oracle here — these assert the relational contract directly with
// exact exit codes, in the rc_heap_bump_* style the native suite established.
// Every result stays ≤ 120 (wasmtime exit-code clamp #2908). Each allocating
// literal takes a runtime element, since a literal of constants is a static box.
var heapBumpBytesIRCases = []struct {
	name string
	main string
	want int
}{
	// Before any allocation the high-water mark is 0.
	{"zero-before-alloc", `function main(): i32 { if ((__heap_bump_bytes() as i32) == 0) { return 7; } return 1; }`, 7},
	// A fresh allocation advances the cursor above the zero baseline.
	{"grows-on-alloc", `function main(): i32 { let before: i32 = (__heap_bump_bytes() as i32); let a: i32[] = [before, 2, 3, 4, 5]; let after: i32 = (__heap_bump_bytes() as i32); if (before == 0) { if (after > before) { return 7; } } return 1; }`, 7},
	// Read across a call boundary + an explicit "after > 0" check.
	{"after-positive", `function main(): i32 { let a: i32[] = [__heap_bump_bytes() as i32, 2, 3]; if ((__heap_bump_bytes() as i32) > 0) { return 11; } return 1; }`, 11},
	// The probe's declared result is i64 on the self-host too, not just
	// natively: the arena is 16 GiB, so the mark passes 2^31 on a long run and
	// an i32 result reads it back negative. Binding it to i64 locals and
	// comparing in 64 bits is the type assertion — if either compiler narrows
	// it again this stops compiling rather than quietly returning a wrapped
	// number. It also covers the wasm zero-extend, whose i32 cursor difference
	// has to reach the i64 the builtin promises.
	{"i64-typed", `function main(): i32 { let before: i64 = __heap_bump_bytes(); let a: i32[] = [before as i32, 2, 3]; let after: i64 = __heap_bump_bytes(); if (before != (0 as i64)) { return 1; } if (after <= before) { return 2; } return 9; }`, 9},
}

// TestSelfHostHeapBumpBytesIR runs each case through the self-host CLI on
// x86-64 and wasm (the `$heap − heap_base` lowering) against each row's
// expected exit code; the rows are the oracle, since the interpreter has no
// bump-allocator model.
func TestSelfHostHeapBumpBytesIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}

// heapBumpFixpointCases pin the FIXPOINT contract the native rc_heap_bump_*
// suite asserts (internal/e2e/rc_heap_bump_*_test.go): a loop whose per-
// iteration allocation is reclaimed reports the SAME bump-growth at N=50 and
// N=5000 — the steady-state high-water is bounded, not scaling with the trip
// count. Each case's `src(n)` returns `__heap_bump_bytes() - before` after an
// N-iteration loop; the growth (an exit code) must match small-vs-large.
//
// #4365 gating: ONLY shapes empirically BOUNDED on the self-host IR path belong
// here. Three Perceus behaviors the self-host DOES have bound their loops
// (verified flat across N=50..5000): precise-drop of a declared owned array
// local, prior-box release on a loop reassign, and a literal-sized owned buffer
// temp (`__alloc_u8(8)`). NOT yet bounded on the self-host IR path, so tracked
// as gaps under #4365 rather than asserted here: the native suite's
// discarded-bare-expr shapes (statement-temporary reclamation), the
// struct-with-array-field reclaim, and a rebuilt generic-enum array
// (`Option[i32[]][]`) all leak with N (verified: discarded-arr 128→leak,
// generic-enum-array 160→224). A value-consuming-op receiver (`(a + b).len()`)
// plateaus at 48 for N>=200 but reads 176 at N=50 — a freelist-warmup artifact,
// not a clean fixpoint at the low end — so it too stays out.
var heapBumpFixpointCases = []struct {
	name string
	src  func(n string) string
}{
	// Precise-drop of a declared owned array local, last-used as borrow reads:
	// its fresh rc=1 box returns to the freelist each iteration (precise_drop_
	// names), so the high-water above `before` is one box regardless of N. The
	// `acc` guard proves the reads see the right values (a wrongly-freed box
	// would corrupt them), so this is bounded AND value-correct.
	{"precise-drop-array", func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32[] = [i, i + 1, i + 2]; acc = acc + v[0] + v[1] + v[2]; i = i + 1; }
    if (acc == 987654) { return 200; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// Loop-reassigned array local: each rebind releases the prior iteration's
	// box (emit_arr_store's prior-value release) before storing the fresh one,
	// so the loop's high-water stays one box wide.
	{"loop-reassign-array", func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32[] = [i, i + 1]; v = [i, i + 2, i + 3]; acc = acc + v[0] + v[1] + v[2]; i = i + 1; }
    if (acc == 987654) { return 200; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// Literal-sized owned buffer (`__alloc_u8(8)`) whose only borrowed input is
	// the literal size arg — its fresh rc=1 buffer is reclaimed at its last use
	// each iteration, so the high-water is flat across N (native + self-host
	// both bounded — #4365 rc_heap_bump_literal_alloc port). The `acc` guard
	// keeps the borrow reads live so a wrongly-freed buffer would corrupt them.
	{"literal-alloc", func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let b: u8[] = __alloc_u8(8); b = b.with(0, (i % 200) as u8); acc = acc + (b[0] as i32); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
}

// TestSelfHostHeapBumpFixpointX86_64 asserts the bounded-high-water fixpoint on
// the self-host x86-64 IR path: each shape's growth at N=50 must equal its
// growth at N=5000 (reclaimed loops don't grow with the trip count), and the
// growth must be non-zero, so a probe that allocates nothing cannot pass
// vacuously. The RELATION is the contract; no absolute figure is.
func TestSelfHostHeapBumpFixpointX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../compiler", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	// shGrowth compiles `prog` through the self-host IR driver, runs it, and
	// returns its exit code (the loop's bump-growth).
	shGrowth := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	const small, large = "50", "5000"
	for _, tc := range heapBumpFixpointCases {
		t.Run(tc.name, func(t *testing.T) {
			// Self-host IR path must reproduce the fixpoint.
			shS := shGrowth(t, tc.name+"-50", tc.src(small))
			shL := shGrowth(t, tc.name+"-5000", tc.src(large))
			if shS != shL {
				t.Errorf("%s: self-host high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, shS, shL)
			}
			if shS == 0 {
				t.Errorf("%s: self-host growth is 0 — nothing allocated / measured", tc.name)
			}
		})
	}
}
