// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_asm_test.go.
package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteSelfHostAsmProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// asm_arm64.fern is in the base set because asm_load_run.fern imports it
	// (since #4506 folded the arm64 loader mirror behind `-target`, so the one
	// loader driver dispatches to either backend). Consumers that build
	// asm_load_run through this helper need it in the temp dir for modload to
	// resolve; consumers building asm.fern (x86) just ignore the extra source.
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "treeshake.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}
