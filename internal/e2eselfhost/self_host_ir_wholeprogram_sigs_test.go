package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRWholeProgramSignatures exercises whole-program signature
// analysis for per-module emit (#3451 step 2 / #3454). Per-module, a call from
// the entry into an imported function is tagged by the CALLEE's return type so a
// chained method on the result lowers correctly — but the callee lives in a
// sibling module, invisible to the entry's own funcs. The driver threads the
// union of every loaded module's signatures into emit; asm_ir_run models that
// with `-ir-sigs <file>` (parse a sibling for signatures only).
//
// Library B's bgreet returns a STRING ("hi"); entry A does `var s = bgreet();
// return s.len();`. Only with B's signature in the whole-program view does the
// entry tag bgreet's result as a string and lower `.len()` to str_len → 2.
// Without it the call is mis-tagged i32 and the emitted asm differs (proving
// the signature actually drives codegen, not just eligibility).
func TestSelfHostIRWholeProgramSignatures(t *testing.T) {
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

	libSrc := "function bgreet(): string { return \"hi\"; }"
	entrySrc := "function main(): i32 { var s = bgreet(); return s.len(); }"

	// Sibling source on disk for -ir-sigs (absolute path so the driver's
	// read_file resolves it regardless of cwd).
	sigPath := filepath.Join(dir, "bmod.fern")
	if err := os.WriteFile(sigPath, []byte(libSrc), 0o644); err != nil {
		t.Fatalf("write bmod.fern: %v", err)
	}

	// The library needs heap (it allocates the "hi" box); aggregate into entry.
	libNeeds := splitNeeds(run(t, libSrc, "-ir-needs"))

	entryArgs := []string{"-ir-unit", "entry", "-ir-ns", "a", "-ir-extern", "bgreet", "-ir-sigs", sigPath}
	for _, n := range libNeeds {
		entryArgs = append(entryArgs, "-ir-extra-need", n)
	}
	entryWith := run(t, entrySrc, entryArgs...)

	// Without the sibling signature, bgreet's return type is invisible, so the
	// call is tagged i32 and the codegen differs — the signature drives emit.
	entryWithout := run(t, entrySrc, "-ir-unit", "entry", "-ir-ns", "a", "-ir-extern", "bgreet")
	if entryWith == entryWithout {
		t.Fatalf("whole-program signature did not change codegen — bgreet's string return type was not threaded into tagging")
	}
	if !strings.Contains(entryWith, "__fern_str") && !strings.Contains(entryWith, "8(%r") {
		// With the signature the result is treated as a string box (len read at
		// offset 8) — a loose sanity check that str handling is present.
		t.Logf("entry-with-sigs asm (head):\n%s", firstNLines(entryWith, 40))
	}

	libAsm := run(t, libSrc, "-ir-unit", "lib", "-ir-ns", "b")

	entryPath := filepath.Join(dir, "wp_entry.s")
	libPath := filepath.Join(dir, "wp_lib.s")
	binPath := filepath.Join(dir, "wp_prog")
	if err := os.WriteFile(entryPath, []byte(entryWith), 0o644); err != nil {
		t.Fatalf("write entry.s: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libAsm), 0o644); err != nil {
		t.Fatalf("write lib.s: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", entryPath, libPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link failed: %v\n%s", err, out)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Errorf("whole-program-signature binary exit = %d, want 2 (len \"hi\")", code)
	}
}
