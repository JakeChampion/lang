package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSelfHostIRRuntimeNeedsAggregation(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "airun")

	run := func(t *testing.T, prog string, args ...string) string {
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
			t.Fatalf("driver failed (args %v) for %q: %v", args, prog, err)
		}
		return string(out)
	}

	libSrc := "function bcat(): i32 { let a = \"ab\"; let b = a + a; return b.len(); }"
	entrySrc := "function main(): i32 { return bcat(); }"

	sigPath := filepath.Join(dir, "rn_lib.fern")
	if err := os.WriteFile(sigPath, []byte(libSrc), 0o644); err != nil {
		t.Fatalf("write rn_lib.fern: %v", err)
	}

	libNeeds := splitNeeds(run(t, libSrc, "-ir-needs", "-ir-ns", "b"))
	if !contains(libNeeds, "str_concat") || !contains(libNeeds, "heap") {
		t.Fatalf("lib needs probe missing str_concat/heap: %v", libNeeds)
	}
	entryNeeds := splitNeeds(run(t, entrySrc, "-ir-needs", "-ir-sigs", sigPath))
	if contains(entryNeeds, "str_concat") {
		t.Fatalf("entry should not itself need str_concat: %v", entryNeeds)
	}

	entryArgs := []string{"-ir-unit", "entry", "-ir-ns", "a", "-ir-extern", "bcat", "-ir-sigs", sigPath}
	for _, n := range libNeeds {
		entryArgs = append(entryArgs, "-ir-extra-need", n)
	}
	entryAsm := run(t, entrySrc, entryArgs...)
	if !strings.Contains(entryAsm, "__fern_str_concat:") {
		t.Fatalf("entry unit did not emit aggregated __fern_str_concat runtime\n%s", rodataOf(entryAsm))
	}
	libAsm := run(t, libSrc, "-ir-unit", "lib", "-ir-ns", "b")

	entryPath := filepath.Join(dir, "rn_entry.s")
	libPath := filepath.Join(dir, "rn_lib.s")
	binPath := filepath.Join(dir, "rn_prog")
	if err := os.WriteFile(entryPath, []byte(entryAsm), 0o644); err != nil {
		t.Fatalf("write entry.s: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libAsm), 0o644); err != nil {
		t.Fatalf("write lib.s: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", entryPath, libPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link failed (aggregation missing a helper?): %v\n%s", err, out)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 4 {
		t.Errorf("aggregated per-module binary exit = %d, want 4 (len \"abab\")", code)
	}
}

func splitNeeds(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}
