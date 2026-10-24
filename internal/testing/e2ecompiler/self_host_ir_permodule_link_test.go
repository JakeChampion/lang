package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostIRPerModuleLink(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "airun")

	emit := func(t *testing.T, prog string, args ...string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit failed (args %v) for %q: %v", args, prog, err)
		}
		return string(out)
	}

	libSrc := "function bfoo(x: i32): i32 { return x * 7; }"
	libAsm := emit(t, libSrc, "-ir-unit", "lib")
	if !strings.Contains(libAsm, ".globl __fn_bfoo") {
		t.Fatalf("lib unit did not export __fn_bfoo as .globl\n--- lib.s ---\n%s", libAsm)
	}
	if strings.Contains(libAsm, "_start:") {
		t.Fatalf("lib (non-entry) unit must not emit _start\n--- lib.s ---\n%s", libAsm)
	}

	sigPath := filepath.Join(dir, "pm_lib.fern")
	if err := os.WriteFile(sigPath, []byte(libSrc), 0o644); err != nil {
		t.Fatalf("write pm_lib.fern: %v", err)
	}
	entryAsm := emit(t, "function main(): i32 { return bfoo(6); }", "-ir-unit", "entry", "-ir-extern", "bfoo", "-ir-sigs", sigPath)
	if !strings.Contains(entryAsm, "call __fn_bfoo") {
		t.Fatalf("entry unit did not reference __fn_bfoo as an extern call\n--- entry.s ---\n%s", entryAsm)
	}
	if !strings.Contains(entryAsm, "_start:") {
		t.Fatalf("entry unit must emit _start\n--- entry.s ---\n%s", entryAsm)
	}

	entryPath := filepath.Join(dir, "pm_entry.s")
	libPath := filepath.Join(dir, "pm_lib.s")
	binPath := filepath.Join(dir, "pm_prog")
	if err := os.WriteFile(entryPath, []byte(entryAsm), 0o644); err != nil {
		t.Fatalf("write entry.s: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libAsm), 0o644); err != nil {
		t.Fatalf("write lib.s: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", entryPath, libPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link two units failed: %v\n%s", err, out)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("linked per-module binary exit = %d, want 42 (6*7)", code)
	}
}
