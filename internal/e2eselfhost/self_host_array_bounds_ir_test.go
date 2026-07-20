package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArrayBoundsIR pins the array-bounds contract
// (docs/ARRAY-BOUNDS.md) on the self-host x86-64 IR path: an out-of-range
// index — read or write, over-large or negative — ABORTS with exit 134 and
// never returns a garbage value. Before this, op_arr_get / op_arr_set emitted a
// raw `data+(idx+1)*8` load/store with no length check, so the self-host
// silently read/wrote past the end (e.g. `a[5]` on a len-3 array returned 0)
// while the native backends aborted — a policy violation. The IR emit now
// checks the index against the length prefix (data[0]) with a single unsigned
// compare and branches to the shared __fern_oob_abort (exit 134).
//
// In-range indexing (every case's "ok" leg) is unaffected — the sum probe
// walks the array and returns a real value.
func TestSelfHostArrayBoundsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; return a[5]; }`, 134},
		{"read-negative",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var i: i32 = 0 - 1; return a[i]; }`, 134},
		{"read-at-len",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; return a[3]; }`, 134},
		{"write-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.with(5, 9); return a[0]; }`, 134},
		// In-range: every element read + a write, no abort. 10+20+30 -> exit 60.
		{"in-range-ok",
			`function main(): i32 { var a: i32[] = [10, 20, 30]; a = a.with(1, 20); var s: i32 = 0; var i: i32 = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }`, 60},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: fell back to the AST path (no .Lir_ labels)", tc.name)
			}
			if tc.want == 134 && !strings.Contains(string(asm), "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted asm — bounds check missing", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostArrayBoundsIRArm64 — CI-gated arm64 counterpart of
// TestSelfHostArrayBoundsIR. The self-host arm64 IR emitter (asm_arm64_ir.fern
// kind_tag 88/89) now bounds-checks arr_get / arr_set the same way as x86-64:
// an unsigned cmp of the index against the length prefix (`ldr x2, [x0]`) and a
// `b.hs __fern_oob_abort` (exit 134). Verified end-to-end under qemu.
func TestSelfHostArrayBoundsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; return a[5]; }`, 134},
		{"read-negative",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var i: i32 = 0 - 1; return a[i]; }`, 134},
		{"write-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.with(5, 9); return a[0]; }`, 134},
		{"in-range-ok",
			`function main(): i32 { var a: i32[] = [10, 20, 30]; a = a.with(1, 20); var s: i32 = 0; var i: i32 = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }`, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			if tc.want == 134 && !strings.Contains(string(asm), "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted arm64 asm — bounds check missing", tc.name)
			}
			progBin := buildBin(t, arm64gcc, dir, "arr_bounds_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
