package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
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
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostParserProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "parser.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port parser assertion %d failed", code)
	}
}

func TestSelfHostParserArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostParserProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "parser.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port parser assertion %d failed", code)
	}
}
