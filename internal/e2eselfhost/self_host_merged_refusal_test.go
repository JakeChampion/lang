package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMergedBundleRefusedByDefaultX86_64 pins the per-module-or-error
// swap (#3457 slice 3).
//
// The whole-compiler self-compile is the last construct on x86 that reaches the
// legacy AST emitter: its merged bundle is ~2040 functions, past the merged IR
// budget (`asm_ir.emit_module_ir_gated`'s 512) AND past the upper bound of
// asm_modload_run's single-process per-module rescue (1500, which exists because
// a concat that large OOMs). It used to fall through to `asm.emit_module` and
// emit AST silently — so the AST emitter stayed reachable without any caller
// saying so, and a new caller could land on it unnoticed.
//
// It is now an error naming the batched `-per-module-emit-all` alternative, with
// `-merged` as the explicit opt-out. Two properties are worth pinning together,
// because either alone is satisfiable by a broken implementation: the default
// must REFUSE (a silent AST emit is the regression), and `-merged` must still
// WORK (the env-gated merged fixpoints and the per-module smoke run depend on it,
// and a refusal that cannot be opted out of would break them).
func TestSelfHostMergedBundleRefusedByDefaultX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	entry := filepath.Join(dir, "asm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "mrefuse")

	run := func(args ...string) (string, string, int) {
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, _ := cmd.Output()
		return string(out), errBuf.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("default-refuses", func(t *testing.T) {
		out, errOut, code := run()
		if code == 0 {
			t.Fatalf("compiling the whole compiler through the merged bundle exited 0, want a refusal (emitted %d bytes)", len(out))
		}
		if len(out) != 0 {
			t.Errorf("refusal still wrote %d bytes of asm to stdout; it must emit nothing", len(out))
		}
		// The message has to name the way forward, not just say no — this is the
		// error a future caller hits, and "past the budget" alone leaves them stuck.
		if !strings.Contains(errOut, "-per-module-emit-all") {
			t.Errorf("refusal does not name the batched alternative:\n%s", errOut)
		}
		if !strings.Contains(errOut, "-merged") {
			t.Errorf("refusal does not name the opt-out:\n%s", errOut)
		}
	})

	t.Run("merged-opt-in-still-works", func(t *testing.T) {
		out, errOut, code := run("-merged")
		if code != 0 {
			t.Fatalf("-merged exited %d, want 0:\n%s", code, errOut)
		}
		if len(out) == 0 {
			t.Fatal("-merged emitted 0 bytes")
		}
		// A real self-compile, not the no-main fallback — the same property the
		// per-module smoke run asserts about this output.
		if !strings.Contains(out, "call __fn_main") {
			t.Error("-merged output missing `call __fn_main` — not a real whole-compiler emit")
		}
	})
}
