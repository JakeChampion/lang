package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4380 lever 3, self-host slice C: the parser's elide_len_bounded_body pass
// marks `arr[i]` READS inside a `while (i < arr.len())` / `while (i < len(arr))`
// loop Unchecked when `0 <= i < len` is syntactically provable, so irlower emits
// op_arr_get_nc (no per-iteration bounds check + len reload). The pass runs at
// lower_func entry, so it is shared by every IR backend: x86-64, wasm, and arm64
// (the latter two already lower the _nc op from slice B). These programs must
// exit with the interpreter-oracle value with the checks elided.
var boundsElideCases = []struct {
	name string
	main string
}{
	// Plain while-sum: 3+5+7+11+13 = 39. The canonical elided shape.
	{"while_sum", `function main(): i32 {
    var xs: i32[] = [3, 5, 7, 11, 13];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s;
}`},
	// Step by 2: indices 0,2,4 of [1,100,2,200,3] = 1+2+3 = 6. Monotonic +2.
	{"while_step_two", `function main(): i32 {
    var xs: i32[] = [1, 100, 2, 200, 3];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 2; }
    return s;
}`},
	// Read nested in an `if` inside the body: evens of [4,9,2,7,6,1] = 12. Marking
	// descends into the nested block (which does not assign i).
	{"nested_if_access", `function main(): i32 {
    var xs: i32[] = [4, 9, 2, 7, 6, 1];
    var e: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { if (xs[i] % 2 == 0) { e = e + xs[i]; } i = i + 1; }
    return e;
}`},
	// Two independent len-bounded loops in the same function, each with its own
	// index: 2+3+4 + 10+20+30+40 = 109. Both elide.
	{"two_loops", `function main(): i32 {
    var xs: i32[] = [2, 3, 4];
    var ys: i32[] = [10, 20, 30, 40];
    var a: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { a = a + xs[i]; i = i + 1; }
    var j: i32 = 0;
    while (j < ys.len()) { a = a + ys[j]; j = j + 1; }
    return a;
}`},
	// i64 elements exercise op_arr_get_i64_nc: 10+20+30+40 = 100.
	{"i64_elems", `function main(): i32 {
    var xs: i64[] = [10, 20, 30, 40];
    var s: i64 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s as i32;
}`},
	// arr reassigned in the body → NOT elided; result stays correct (only xs[0]=5
	// is read before `xs = ys` shrinks the loop). Guards the arr-invariant bail.
	{"arr_reassigned_not_elided", `function main(): i32 {
    var xs: i32[] = [5, 6, 7];
    var ys: i32[] = [9];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; xs = ys; i = i + 1; }
    return s;
}`},
}

// TestSelfHostBoundsElideIRX86_64 asserts each case (1) lowers through the IR
// path and (2) exits with the interp-oracle value with checks elided; plus a
// differential that pins the elision actually fired (an elidable
// `while (i < xs.len())` emits strictly one fewer __fern_oob_abort than a twin
// whose bound `n` hides the len) and a safety guard (a read AFTER the increment
// stays checked and traps).
func TestSelfHostBoundsElideIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(src string) string {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		asm, err := cmd.Output()
		if err != nil || len(asm) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		return string(asm)
	}
	runExit := func(t *testing.T, bin string) int {
		var run *exec.Cmd
		if len(runner) == 0 {
			run = exec.Command(bin)
		} else {
			run = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = run.Run()
		return run.ProcessState.ExitCode()
	}

	for _, tc := range boundsElideCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			asm := emit(src)
			if !strings.Contains(asm, ".Lir_") {
				t.Fatalf("%s: fell back to the AST path (no .Lir_ labels)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "belide_"+tc.name, asm)
			if code := runExit(t, bin); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}

	// Elision-fired differential: the elidable guard drops exactly one bounds
	// check (one fewer __fern_oob_abort) vs a twin whose bound is a separate `n`.
	t.Run("elision_fired_differential", func(t *testing.T) {
		elide := `function main(): i32 { var xs: i32[] = [3, 5, 7, 11, 13]; var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }`
		noElide := `function main(): i32 { var xs: i32[] = [3, 5, 7, 11, 13]; var n: i32 = xs.len(); var s: i32 = 0; var i: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`
		got := strings.Count(emit(elide), "__fern_oob_abort")
		base := strings.Count(emit(noElide), "__fern_oob_abort")
		if got >= base {
			t.Fatalf("elision did not fire: elidable emitted %d __fern_oob_abort, non-elidable %d (want fewer)", got, base)
		}
		// Both must still compute the right sum (39).
		for name, prog := range map[string]string{"elide": elide, "noElide": noElide} {
			if code := runExit(t, buildBin(t, gcc, dir, "beldiff_"+name, emit(prog))); code != 39 {
				t.Errorf("%s exited %d, want 39", name, code)
			}
		}
	})

	// Safety: a read AFTER the increment is NOT marked (i can reach len), so the
	// checked op still traps (exit 134) instead of reading past the end.
	t.Run("read_after_increment_stays_checked", func(t *testing.T) {
		src := `function main(): i32 { var xs: i32[] = [1, 2, 3]; var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { i = i + 1; s = s + xs[i]; } return s; }`
		bin := buildBin(t, gcc, dir, "belide_after_incr", emit(src))
		if code := runExit(t, bin); code != 134 {
			t.Errorf("read-after-increment exited %d, want 134 (bounds check must remain)", code)
		}
	})
}

// TestSelfHostBoundsElideIRWasm runs the correctness cases through the wasm IR
// backend — the elision lives in irlower (target-independent) and wasm_ir.fern
// already lowers op_arr_get_nc (slice B), so wasm gets it for free. Interp oracle.
func TestSelfHostBoundsElideIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host bounds-elide wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range boundsElideCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = strings.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "belide_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("bounds-elide wasm IR %q = %d, want %d", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostBoundsElideIRArm64 — CI-gated arm64 counterpart: asm_arm64_ir.fern
// already lowers op_arr_get_nc (slice B), so the same irlower marking elides the
// while-loop reads on arm64 too. Verified under qemu.
func TestSelfHostBoundsElideIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range boundsElideCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			progBin := buildBin(t, arm64gcc, dir, "belide_arm64_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
