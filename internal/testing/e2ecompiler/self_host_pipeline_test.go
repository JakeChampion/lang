package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

// Pipeline orchestrator — the "everything composes" demo. Imports
// every layer in the fern-port (lexer + parser + constfold +
// checker + interp) and drives a non-trivial source through them
// end-to-end:
//
//   bytes → Token[] → Module → folded Module → ModuleTypes → Value
//
// Each layer was already exercised individually by its own
// main(); this file glues them together and asserts the composed
// pipeline produces the right answer. Catches mis-wired imports,
// accidentally-broken signatures, and "I changed one layer and
// forgot to re-test the chain" classes of bugs.
//
// main() runs five sub-checks:
//   1. Recursion + const-fold opportunity: fact(2+3) = 120.
//   2. Constfold visible in the AST: let c = 2 + 3 → ExprNumber "5".
//   3. Ill-typed program — checker rejects, the interpreter is never called.
//   4. Array + while: sum of [3,5,7,9] = 24 via main().
//   5. No-function top-level: let x = 7; let y = 11; return x+y → 18.

func writeSelfHostPipelineProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "constfold.fern", "checker.fern", "interp.fern", "drivers/pipeline.fern")
	return dir
}

func TestSelfHostPipelineX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostPipelineProject(t)
	binPath := buildSelfHostBinFor(t, dir, "drivers/pipeline.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port pipeline assertion %d failed", code)
	}
}

func TestSelfHostPipelineArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostPipelineProject(t)
	binPath := buildSelfHostBinFor(t, dir, "drivers/pipeline.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port pipeline assertion %d failed", code)
	}
}
