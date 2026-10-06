// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/self_host_modload_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "drivers/irverifyprovided.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "flatten.fern", "modloader.fern", "fern_toml.fern", "builtins.fern", "drivers/asm_modload_run.fern", "treeshake.fern", "drivers/rundriver.fern")
	return dir
}

// WriteSelfHostModloadProjectTyped is WriteSelfHostModloadProject for a test
// whose driver compiles the compiler's own sources over the typed lowering,
// which reads the stdlib modules' bodies.
func WriteSelfHostModloadProjectTyped(t *testing.T) string {
	t.Helper()
	dir := WriteSelfHostModloadProject(t)
	CopyStdlibTree(t, dir)
	return dir
}

// CopyStdlibTree copies the stdlib source tree into dst, where a program at
// dst's root resolves `std/…` and `core/…` imports.
func CopyStdlibTree(t *testing.T, dst string) {
	t.Helper()
	src := SelfHostStdlibRoot(t)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !strings.HasSuffix(path, ".fern") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy stdlib tree: %v", err)
	}
}

// RunDriverFile runs the compiled driver binary with `entry` as argv[1]
// (plus any extra driver flags, e.g. "-target", "arm64-linux") and returns its
// stdout (the emitted asm).
func RunDriverFile(t *testing.T, runner []string, bin, entry string, extraArgs ...string) []byte {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin, append([]string{entry}, extraArgs...)...)
	out, err := cmd.Output()
	if err != nil {
		// The driver reports a refusal or a diagnostic on stderr, which
		// Output() captures but does not put in the error. Without it a
		// compile failure reads only as "exit status 1".
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("run driver on %s: %v\n%s", entry, err, stderr)
	}
	return out
}
