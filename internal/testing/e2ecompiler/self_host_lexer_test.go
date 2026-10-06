package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

// First step of the self-host port: `compiler/lexer.fern`
// is the Go lexer (`internal/syntax/lexer/lexer.go`) re-written in lang.
// Validates the language can express the lexer's logic end-to-end:
// union types for Token kinds, generic-shaped helpers, struct
// methods, mutual recursion (used in skip_trivia / advance via
// self-referential method calls), match with literal patterns
// over the Token union, byte-level string slicing for the
// scan_* routines.
//
// The .fern file's `main()` runs the lexer on a mixed-token input
// (keyword + ident + multi-char punct + integer suffix + string
// + line comment + float suffix + position tracking across a `\n`)
// and asserts the produced Token[] step-by-step. Exit code 0
// means every assertion passed; non-zero codes identify which
// arm failed.
func TestSelfHostLexerX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lexer.fern")
	binPath := buildSelfHostBinFor(t, dir, "lexer.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port lexer assertion %d failed", code)
	}
}

func TestSelfHostLexerArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lexer.fern")
	binPath := buildSelfHostBinFor(t, dir, "lexer.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port lexer assertion %d failed", code)
	}
}
