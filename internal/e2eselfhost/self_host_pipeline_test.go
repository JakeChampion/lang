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
//   1. Mutual recursion + const-fold opportunity: fact(2+3) = 120.
//   2. Constfold visible in the AST: var c = 2 + 3 → ExprNumber "5".
//   3. Ill-typed program — checker rejects, the interpreter is never called.
//   4. Array + while: sum of [3,5,7,9] = 24 via main().
//   5. No-function top-level: var x = 7; var y = 11; return x+y → 18.

func writeSelfHostPipelineProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "constfold.fern", "checker.fern", "interp.fern", "pipeline.fern"}
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestSelfHostPipelineX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostPipelineProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "pipeline.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
		t.Errorf("fern-port pipeline assertion %d failed", code)
	}
}

func TestSelfHostPipelineArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostPipelineProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "pipeline.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
		t.Errorf("fern-port pipeline assertion %d failed", code)
	}
}
