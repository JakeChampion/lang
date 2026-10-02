// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_asm_test.go.
package e2eharness

import (
	"testing"
)

func WriteSelfHostAsmProject(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	// rundriver.fern is in the base set because every stdin driver imports it
	// for the shared parse_stdin preamble, and 211 tests stage a driver by
	// hand-copying the one file instead of going through CopySelfHostDriver
	// (which would expand the closure for them).
	//
	// asm_arm64_ir.fern and semlower.fern are there for the drivers staged the
	// same way: asm_load_run dispatches to either backend behind `-target`, and
	// asm_run, asm_load_run and asm_ir_run lower through the typed path.
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "fnsigs.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "treeshake.fern", "rundriver.fern", "semlower.fern")
	return dir
}
