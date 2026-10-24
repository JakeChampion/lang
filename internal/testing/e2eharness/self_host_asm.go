package e2eharness

import (
	"testing"
)

func WriteSelfHostAsmProject(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "treeshake.fern", "drivers/rundriver.fern", "semlower.fern")
	return dir
}
