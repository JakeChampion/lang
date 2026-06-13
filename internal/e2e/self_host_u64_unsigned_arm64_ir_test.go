package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU64UnsignedArm64IR is the arm64 sibling of
// TestSelfHostU64UnsignedIR: it proves the arm64 stack-IR backend
// (asm_arm64_ir.fern) lowers u64 as UNSIGNED in the 64-bit domain (#2904) —
// unsigned compares via the lo/ls/hi/hs condition codes, logical `>>` via lsr,
// and `/`/`%` via udiv. The oracle is the NATIVE arm64 compiler
// (compileAndRunArm64); for a bit-63-set program IR == native also proves the
// program took the IR path. CI-gated arm64 (qemu). Reuses u64UnsignedCases.
func TestSelfHostU64UnsignedArm64IR(t *testing.T) {
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

	for _, tc := range u64UnsignedCases {
		t.Run(tc.name, func(t *testing.T) {
			_, want := compileAndRunArm64(t, tc.src) // native arm64 = correct oracle
			if got := emitAndRunIR(t, tc.src); got != want {
				t.Errorf("self-host arm64 IR u64 %q: exit = %d, want %d (native)", tc.name, got, want)
			}
		})
	}
}
