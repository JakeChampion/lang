// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_modload_test.go.
package e2eharness

import (
	"testing"
)

// WriteSelfHostModloadProject lays out the self-host sources needed to
// build the import-driven driver (asm_modload_run.fern): the asm pipeline
// plus flatten + the real builtins module + the driver itself.
func WriteSelfHostModloadProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// treeshake backs the over-budget per-module rescue: the driver derives
	// the reachable-name set from it before pruning each unit.
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifyprovided.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "flatten.fern", "modloader.fern", "fern_toml.fern", "builtins.fern", "asm_modload_run.fern", "treeshake.fern")
	return dir
}

// RunDriverFile runs the compiled driver binary with `entry` as argv[1]
// (plus any extra driver flags, e.g. "-target", "arm64-linux") and returns its
// stdout (the emitted asm).
func RunDriverFile(t *testing.T, runner []string, bin, entry string, extraArgs ...string) []byte {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin, append([]string{entry}, extraArgs...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run driver on %s: %v", entry, err)
	}
	return out
}
