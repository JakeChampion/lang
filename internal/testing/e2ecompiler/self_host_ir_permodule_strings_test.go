package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostIRPerModuleLinkStrings(t *testing.T) {
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

	libSrc := "function blen(): i32 { let s = \"lib\"; return s.len(); }"
	libAsm := emit(t, libSrc, "-ir-unit", "lib", "-ir-ns", "b")
	sigPath := filepath.Join(dir, "pms_lib.fern")
	if err := os.WriteFile(sigPath, []byte(libSrc), 0o644); err != nil {
		t.Fatalf("write pms_lib.fern: %v", err)
	}
	entryAsm := emit(t, "function main(): i32 { let s = \"entrypoint\"; return s.len() + blen(); }", "-ir-unit", "entry", "-ir-ns", "a", "-ir-extern", "blen", "-ir-sigs", sigPath)

	if !strings.Contains(entryAsm, ".Sa_0:") {
		t.Fatalf("entry string label not namespaced (want .Sa_0:)\n--- entry.s rodata ---\n%s", rodataOf(entryAsm))
	}
	if !strings.Contains(libAsm, ".Sb_0:") {
		t.Fatalf("lib string label not namespaced (want .Sb_0:)\n--- lib.s rodata ---\n%s", rodataOf(libAsm))
	}
	if strings.Contains(entryAsm, ".Sb_0:") || strings.Contains(libAsm, ".Sa_0:") {
		t.Fatalf("string labels leaked across modules\n--- entry ---\n%s\n--- lib ---\n%s", rodataOf(entryAsm), rodataOf(libAsm))
	}

	entryPath := filepath.Join(dir, "pms_entry.s")
	libPath := filepath.Join(dir, "pms_lib.s")
	binPath := filepath.Join(dir, "pms_prog")
	if err := os.WriteFile(entryPath, []byte(entryAsm), 0o644); err != nil {
		t.Fatalf("write entry.s: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libAsm), 0o644); err != nil {
		t.Fatalf("write lib.s: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", entryPath, libPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link two string-using units failed: %v\n%s", err, out)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 13 {
		t.Errorf("linked per-module string binary exit = %d, want 13 (10+3)", code)
	}
}

func rodataOf(asm string) string {
	i := strings.Index(asm, ".rodata")
	if i < 0 {
		return "(no .rodata section)"
	}
	return asm[i:]
}
