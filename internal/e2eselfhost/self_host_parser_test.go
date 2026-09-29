package e2eselfhost

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"testing"
)

// Second step of the self-host port: `examples/self_host/parser.fern`
// is a recursive-descent parser written in lang, layered on top of
// `examples/self_host/lexer.fern` via `import "./lexer"` — the
// cross-module qualified variant patterns from #615 are what let the
// parser pattern-match `lexer.TokIdent(x) => …` against the lexer's
// Token union. Together they exercise: union types over Token *and*
// Expr/Stmt, struct methods with implicit struct→union return-position
// wrap, precedence climbing, recursive parser combinators that thread
// parser state via value semantics, nested `match` over union variants
// across module boundaries inside the validation harness.
//
// The .fern file's `main()` parses the source
//
//   var x = 1 + 2 * 3; var y = (1 + 2) * 3; return x + y;
//
// and asserts the resulting Stmt[] shape: precedence rules give
// `x = 1 + (2*3)`, parens override to `(1+2) * 3`, and `return x + y`
// is a binary `+` of two idents. Exit code 0 means every assertion
// passed; non-zero codes identify which arm failed.
//
// The test copies both lexer.fern and parser.fern into a temp dir so
// the `import "./lexer"` resolves through modload's normal import
// machinery — same pipeline cmd/fern uses end-to-end.

func writeSelfHostParserProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "parser.fern")
	return dir
}

func TestSelfHostParserX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	dir := writeSelfHostParserProject(t)
	binPath := buildSelfHostBinFor(t, dir, "parser.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port parser assertion %d failed", code)
	}
}

func TestSelfHostParserArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	dir := writeSelfHostParserProject(t)
	binPath := buildSelfHostBinFor(t, dir, "parser.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port parser assertion %d failed", code)
	}
}
