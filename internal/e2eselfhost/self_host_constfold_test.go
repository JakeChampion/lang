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
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostConstfoldProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "constfold.fern"))
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
		t.Errorf("fern-port constfold assertion %d failed", code)
	}
}

func TestSelfHostConstfoldArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostConstfoldProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "constfold.fern"))
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
		t.Errorf("fern-port constfold assertion %d failed", code)
	}
}
