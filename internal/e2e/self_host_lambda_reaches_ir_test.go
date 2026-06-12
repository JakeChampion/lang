package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLambdaReachesIR guards that lifted-lambda programs actually lower
// through the IR path, not the AST fallback. The lift hoists a lambda to a
// top-level __lam_<k> function; the AST backend never produces that symbol (it
// boxes the lambda inline), and the IR path never emits the AST tagged-value
// runtime (release_JNull / …). So "emits __lam_0 AND no AST runtime" ⟺ IR path.
//
// This is the check that was missing while the lambda slices silently rode the
// AST fallback: a native free-list bug corrupted the lift's reconstructed
// statements, the lifted module bailed IR eligibility, and the exit-code-only
// tests passed via AST. The fix (binding each rebuilt statement to a `var`
// before the result array) is in irlower.lift_stmt.
func TestSelfHostLambdaReachesIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping lambda-reaches-IR guard")
	}
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

	// A no-capture lambda passed as a callback — the slice-2a shape.
	src := `function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(function(x: i32): i32 { return x + 1; }, 41); }`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	out := string(wat)
	if !strings.Contains(out, "__lam_0") {
		t.Errorf("lambda program did not reach the IR path: emitted WAT has no __lam_0 (lifted fn)\n%s", out)
	}
	if strings.Contains(out, "release_JNull") {
		t.Errorf("lambda program fell back to the AST backend (AST tagged-value runtime present)")
	}
}
