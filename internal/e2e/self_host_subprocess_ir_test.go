package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSubprocessIR pins `subprocess(cmd, args, stdin)` lowering on the
// self-host x86-64 IR path. subprocess returns the injected ProcessResult STRUCT
// (stdout / stderr / exit_code). It had a full AST runtime (__fern_subprocess —
// 3 pipes, fork, execve with /bin/ + /usr/bin/ PATH fallback, wait4, stdout/stderr
// capture) but no IR lowering, so a program using it bailed `BAIL call[subprocess]`
// -> AST, dragging std/test's process assertions (process_assertions /
// process_output_shortcuts) + lang_binary_e2e to the legacy emitter. It now lowers
// to op_subprocess -> the same __fern_subprocess runtime the AST path calls (x86
// transcribed, with ProcessResult pre-interned via shape_ref before the literal
// pool and __fern_envp shared with env(); arm64 reuses asm_arm64's heap-block
// runtime).
//
// The program spawns `echo hi` (asserting exit_code 0 + the captured stdout) and
// `cat` with a stdin payload (asserting the payload round-trips back through the
// child's stdout), exiting 0 only if both resolve correctly.
func TestSelfHostSubprocessIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("subprocess IR test runs only natively (spawns host commands)")
	}
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// `echo hi` -> stdout "hi\n", exit 0. `cat` with a stdin payload echoes it
	// back through stdout, exit 0. r.exit_code / r.stdout read ProcessResult
	// fields, binding via the injected struct layout.
	src := `function main(): i32 {
    var r: ProcessResult = subprocess("echo", ["hi"], "");
    if (r.exit_code != 0) { return 1; }
    if (r.stdout != "hi\n") { return 2; }
    var c: ProcessResult = subprocess("cat", [], "piped-stdin");
    if (c.exit_code != 0) { return 3; }
    if (c.stdout != "piped-stdin") { return 4; }
    return 0;
}`

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_subprocess") {
		t.Fatal("subprocess did not reach the IR runtime path (no __fern_subprocess in asm)")
	}
	progBin := buildBin(t, gcc, dir, "subprocess_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("subprocess IR program exited %d, want 0 (echo stdout+exit / cat stdin round-trip)", code)
	}
}
