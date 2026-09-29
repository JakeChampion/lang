package e2eselfhost

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"testing"
)

// Sixth self-host milestone, and the first AST→AST transformation
// pass in the port. `constfold.fern` walks a parser.Module and
// rewrites every constant sub-expression to its folded form:
//
//   var a = 1 + 2 * 3;        →  var a = 7;
//   var b = "hi " + "there";  →  var b = "hi there";
//   var c = !true;            →  var c = false;
//
// Up to now every layer (checker, interp, printer) was an AST
// consumer. constfold is the first that rebuilds the tree —
// folding where it can, copying where it can't. That's the
// same pattern the real Go pipeline uses for monomorph /
// closureconv / treeshake / the production constfold pass.
//
// Validation main() runs nine sub-checks: i32 arithmetic
// precedence, comparison → bool, logical && / !, string concat,
// non-constant operands preserved, division-by-zero left
// unfolded for the runtime to surface, nested foldings cascade
// in a single pass, folds inside function bodies.

func writeSelfHostConstfoldProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "constfold.fern")
	return dir
}

func TestSelfHostConstfoldX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	dir := writeSelfHostConstfoldProject(t)
	binPath := buildSelfHostBinFor(t, dir, "constfold.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port constfold assertion %d failed", code)
	}
}

func TestSelfHostConstfoldArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	dir := writeSelfHostConstfoldProject(t)
	binPath := buildSelfHostBinFor(t, dir, "constfold.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port constfold assertion %d failed", code)
	}
}
