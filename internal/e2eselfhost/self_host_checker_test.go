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

// Third step of the self-host port: `examples/self_host/checker.fern`
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
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostCheckerProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "checker.fern"))
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
		t.Errorf("fern-port checker assertion %d failed", code)
	}
}

func TestSelfHostCheckerArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostCheckerProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "checker.fern"))
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
		t.Errorf("fern-port checker assertion %d failed", code)
	}
}
