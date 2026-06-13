package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMutCaptureArm64IR is the arm64 sibling of TestSelfHostMutCaptureIR:
// it proves mutable scalar captures (#2850) work on the arm64 stack-IR backend.
// The boxing pre-pass is backend-agnostic (lift_lambdas), so this exercises the
// arm64 codegen for the resulting array-cell + closure ops. Expected values are
// the Go reference interpreter's (hardcoded — the native COMPILED backend shares
// the by-value bug, so it can't be the oracle; see TestSelfHostMutCaptureIR).
// CI-gated arm64 (qemu).
func TestSelfHostMutCaptureArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		bin := buildBinArm64(t, arm64gcc, dir, "ir_inner", string(emitted))
		inner := runArm64Bin(qemu, bin)
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"write-only", `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`, 49},
		{"counter", `function main(): i32 { var x = 0; var inc = function (): i32 { x = x + 1; return x; }; var a = inc(); var b = inc(); return x; }`, 2},
		{"counter-param", `function main(): i32 { var n = 10; var add = function (d: i32): i32 { n = n + d; return n; }; var a = add(5); var b = add(3); return n; }`, 18},
		{"read-only", `function main(): i32 { var x = 5; var f = function (): i32 { return x + 1; }; return f(); }`, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != tc.want {
				t.Errorf("self-host arm64 IR %q: exit = %d, want %d (reference interp)", tc.name, got, tc.want)
			}
		})
	}
}
