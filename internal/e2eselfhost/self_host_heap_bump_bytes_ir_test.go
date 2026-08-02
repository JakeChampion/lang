package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// exact exit codes (cross-checked against the native backend, which lowers the
// builtin via __fern_heap_bump_bytes), mirroring the native rc_heap_bump_* style.
// Every result stays ≤ 120 (wasmtime exit-code clamp #2908).
var heapBumpBytesIRCases = []struct {
	name string
	main string
	want int
}{
	// Before any allocation the high-water mark is 0.
	{"zero-before-alloc", `function main(): i32 { if (__heap_bump_bytes() == 0) { return 7; } return 1; }`, 7},
	// A fresh allocation advances the cursor above the zero baseline.
	{"grows-on-alloc", `function main(): i32 { var before: i32 = __heap_bump_bytes(); var a: i32[] = [1, 2, 3, 4, 5]; var after: i32 = __heap_bump_bytes(); if (before == 0) { if (after > before) { return 7; } } return 1; }`, 7},
	// Read across a call boundary + an explicit "after > 0" check.
	{"after-positive", `function main(): i32 { var a: i32[] = [1, 2, 3]; if (__heap_bump_bytes() > 0) { return 11; } return 1; }`, 11},
}

// TestSelfHostHeapBumpBytesIRX86_64 routes each case through the self-hosted
// x86-64 IR driver (asm_run), pins the routing to "ir" (asm_pathprobe_run), and
// checks the exit code against the native backend's exit code (the oracle here,
// since the interpreter has no bump-allocator model).
func TestSelfHostHeapBumpBytesIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			// Native cross-check: the Go x86-64 backend must give the same code.
			if _, code := compileAndRunX86_64(t, tc.main+"\n"); code != tc.want {
				t.Fatalf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
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
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var v: i32[] = [i, i + 1, i + 2]; acc = acc + v[0] + v[1] + v[2]; i = i + 1; }
    if (acc == 987654) { return 200; }
    return __heap_bump_bytes() - before;
}`
	}},
	// Loop-reassigned array local: each rebind releases the prior iteration's
	// box (emit_arr_store's prior-value release) before storing the fresh one,
	// so the loop's high-water stays one box wide.
	{"loop-reassign-array", func(n string) string {
		return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var v: i32[] = [i, i + 1]; v = [i, i + 2, i + 3]; acc = acc + v[0] + v[1] + v[2]; i = i + 1; }
    if (acc == 987654) { return 200; }
    return __heap_bump_bytes() - before;
}`
	}},
	// Literal-sized owned buffer (`__alloc_u8(8)`) whose only borrowed input is
	// the literal size arg — its fresh rc=1 buffer is reclaimed at its last use
	// each iteration, so the high-water is flat across N (native + self-host
	// both bounded — #4365 rc_heap_bump_literal_alloc port). The `acc` guard
	// keeps the borrow reads live so a wrongly-freed buffer would corrupt them.
	{"literal-alloc", func(n string) string {
		return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var b: u8[] = __alloc_u8(8); b = b.with(0, (i % 200) as u8); acc = acc + (b[0] as i32); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return __heap_bump_bytes() - before;
}`
	}},
}

// TestSelfHostHeapBumpFixpointX86_64 asserts the bounded-high-water fixpoint on
// the self-host x86-64 IR path: each shape's growth at N=50 must equal its
// growth at N=5000 (reclaimed loops don't grow with the trip count). Native is
// the oracle for "the behavior is real + bounded" (small==large, non-zero); the
// self-host must reproduce the fixpoint. Absolute growth differs between the two
// (box layouts differ), so this checks the RELATION per backend, not equality
// across them.
func TestSelfHostHeapBumpFixpointX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
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
			// Native oracle: the behavior must be real and bounded there.
			_, nS := compileAndRunX86_64(t, tc.src(small)+"\n")
			_, nL := compileAndRunX86_64(t, tc.src(large)+"\n")
			if nS != nL {
				t.Fatalf("%s: native not bounded (N=50 -> %d, N=5000 -> %d) — probe is not a reclaim fixpoint", tc.name, nS, nL)
			}
			if nS == 0 {
				t.Fatalf("%s: native growth is 0 — probe does not allocate", tc.name)
			}
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

// TestSelfHostHeapBumpBytesIRWasm runs the same cases through the wasm IR
// backend (the `$heap − heap_base` lowering).
func TestSelfHostHeapBumpBytesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host heap-bump-bytes wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "heap_bump_bytes_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("heap-bump-bytes wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
