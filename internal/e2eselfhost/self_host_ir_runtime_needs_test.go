package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSelfHostIRRuntimeNeedsAggregation exercises whole-program runtime-needs
// aggregation (#3451 step 3 / #3455). In per-module emit the ENTRY module emits
// the single shared __fern_* runtime, but on its own it only knows its OWN
// needs. A LIBRARY module that uses a helper the entry doesn't (here:
// __fern_str_concat, via `a + b`) would reference an undefined symbol at link.
//
// asm_ir.module_runtime_needs reports a module's closed need-set (`-ir-needs`);
// the driver unions those across modules and passes them to the entry as
// `-ir-extra-need`, so the entry's runtime covers every module. The test would
// FAIL (link error: undefined __fern_str_concat / __fern_alloc) without the
// aggregation, because the entry here allocates/concats nothing itself.
//
// Library B's bcat concatenates "ab"+"ab" and returns its length (4); entry A
// just calls bcat. Linked binary exits 4.
func TestSelfHostIRRuntimeNeedsAggregation(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

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

	libSrc := "function bcat(): i32 { var a = \"ab\"; var b = a + a; return b.len(); }"
	entrySrc := "function main(): i32 { return bcat(); }"

	// The library needs str_concat + heap; the entry needs nothing.
	libNeeds := splitNeeds(run(t, libSrc, "-ir-needs", "-ir-ns", "b"))
	if !contains(libNeeds, "str_concat") || !contains(libNeeds, "heap") {
		t.Fatalf("lib needs probe missing str_concat/heap: %v", libNeeds)
	}
	entryNeeds := splitNeeds(run(t, entrySrc, "-ir-needs"))
	if contains(entryNeeds, "str_concat") {
		t.Fatalf("entry should not itself need str_concat: %v", entryNeeds)
	}

	// Build the entry's -ir-extra-need args from the whole-program union (here
	// just the library's needs) — this is what the driver will do across modules.
	entryArgs := []string{"-ir-unit", "entry", "-ir-ns", "a", "-ir-extern", "bcat"}
	for _, n := range libNeeds {
		entryArgs = append(entryArgs, "-ir-extra-need", n)
	}
	entryAsm := run(t, entrySrc, entryArgs...)
	// The aggregated runtime must now be present in the entry unit.
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
