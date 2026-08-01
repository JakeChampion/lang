package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSubprocessIR pins `subprocess(cmd, args, stdin)` lowering on the
// self-host x86-64 IR path. subprocess spawns a child, pipes its streams, and
// returns a BARE ProcessResult struct {stdout, stderr, exit_code} — unwrapped by
// a Result, unlike stat. It had a full AST runtime (__fern_subprocess) but no IR
// lowering, so it bailed `BAIL call[subprocess]` -> AST, dragging
// process_assertions / process_output_shortcuts / lang_binary_e2e to the legacy
// emitter (#3457). It now lowers to op_subprocess -> the same __fern_subprocess
// runtime the AST path calls.
//
// This exercises the bare-struct-RESULT typing: expr_struct_type types
// `subprocess(..)` as ProcessResult (no match needed), so `var r = subprocess(..)`
// binds r and r.stdout / r.exit_code resolve against the injected struct. The
// program runs /bin/echo (stdout capture), /bin/cat (stdin piping), and a
// nonexistent binary (spawn failure -> exit_code 127), exiting 0 only if all
// three resolve correctly.
func TestSelfHostSubprocessIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("subprocess IR test runs only natively (forks host binaries)")
	}
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    var r = subprocess("/bin/echo", ["hello"], "");
    if (r.exit_code != 0) { return 1; }
    if (r.stdout != "hello\n") { return 2; }
    var c = subprocess("/bin/cat", [], "piped-input");
    if (c.exit_code != 0) { return 3; }
    if (c.stdout != "piped-input") { return 4; }
    var n = subprocess("/nonexistent_binary_xyz", [], "");
    if (n.exit_code != 127) { return 5; }
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
		t.Errorf("subprocess IR program exited %d, want 0 (echo stdout / cat stdin-pipe / missing=127)", code)
	}
}
