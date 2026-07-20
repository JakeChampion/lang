package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSubprocessWasmUnsupported pins #4320: `subprocess` (child-process
// spawning) is unsupportable on wasm/WASI, so the self-host wasm-IR driver must
// reject a program that calls it with a CLEAN diagnostic — a non-zero exit and a
// "subprocess is not supported on the wasm target" message on stderr, emitting NO
// WAT — instead of routing it to the AST emitter (which has no subprocess runtime
// and would produce invalid WAT) or emitting a broken IR module. The `subprocess`
// deferral exclusion in wasm_ir_deferrals_ok is removed; module_uses_subprocess
// gates it in wasm_ir_run before either emit path.
func TestSelfHostSubprocessWasmUnsupported(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	run := func(t *testing.T, src string) (int, string, string) {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = strings.NewReader(src)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
	}

	t.Run("rejects_subprocess_call", func(t *testing.T) {
		code, out, errOut := run(t, `function main(): i32 { var r = subprocess("/bin/echo", [], ""); return r.exit_code; }`+"\n")
		if code == 0 {
			t.Fatalf("expected non-zero exit for a subprocess program, got 0 (stdout %d bytes)", len(out))
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("expected NO WAT on stdout for a rejected subprocess program, got %d bytes", len(out))
		}
		if !strings.Contains(errOut, "subprocess") || !strings.Contains(errOut, "not supported") {
			t.Errorf("stderr %q does not mention subprocess/not supported", errOut)
		}
	})

	// Regression: a program that merely mentions no subprocess still emits WAT
	// (the gate must not reject ordinary modules).
	t.Run("ordinary_module_still_emits", func(t *testing.T) {
		code, out, errOut := run(t, `function main(): i32 { return 42; }`+"\n")
		if code != 0 {
			t.Fatalf("ordinary module rejected: exit %d, stderr %q", code, errOut)
		}
		if !strings.Contains(out, "(module") {
			t.Errorf("ordinary module emitted no WAT module (%d bytes)", len(out))
		}
	})
}
