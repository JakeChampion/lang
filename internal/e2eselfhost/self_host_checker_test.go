package e2eselfhost

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"testing"
)

// Third step of the self-host port: `compiler/checker.fern`
// is a minimal type-checker written in lang. It imports both
// `./lexer` and `./parser` and walks the Stmt[] / Expr tree
// produced by `parser.parse_program(toks)`, assigning a Type
// (TypeI32 / TypeBool / TypeString / TypeUnknown) to every
// expression and threading a flat name → type scope through the
// statement list.
//
// Coverage: primitive types, var binding, binary arithmetic (+ -
// * / %), comparisons, equality, logical &&/||, unary - and !,
// string concatenation via `+`. Out of scope (still): generics,
// unions/enums, structs/methods, function-call typing, control
// flow — the parser stub doesn't emit those yet either.
//
// The .fern file's main() runs four checks: a fully well-typed
// program (i32 + string + bool + bool + i32), a type-mismatch
// (`1 + "x"` → Unknown), forward/backward ident lookup (forward
// → Unknown, backward → i32), and logical/comparison ops. Exit
// code 0 means every assertion passed; non-zero codes identify
// which arm failed.

func writeSelfHostCheckerProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	return dir
}

func TestSelfHostCheckerX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostCheckerProject(t)
	binPath := buildSelfHostBinFor(t, dir, "checker.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port checker assertion %d failed", code)
	}
}

func TestSelfHostCheckerArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostCheckerProject(t)
	binPath := buildSelfHostBinFor(t, dir, "checker.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port checker assertion %d failed", code)
	}
}
