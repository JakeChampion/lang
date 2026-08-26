// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_asm_test.go.
package e2eharness

import (
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
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "treeshake.fern")
	return dir
}
