package e2e

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

// Seventh self-host milestone — first "lower-level
// representation" layer. `vm.fern` compiles a parser.Module's
// top-level stmts to a linear Op[] bytecode stream and then
// executes it on a tiny stack machine. Sits next to the
// tree-walking interp (#623) as a parallel evaluator: same
// inputs, same Value union output, different lowering shape.
//
// Coverage: i32 / bool / string values, var declarations with
// local-index assignment, reassignment via `=`, return,
// if/else (forward-jump patching), while (back-edge + forward
// jump out), binary / unary / comparison / logical ops, expr
// stmts. Function decls + arrays are out of scope for this
// cut — a follow-up would add an OpCall + call frame stack.
//
// Validation main() runs ten sub-checks: arithmetic precedence,
// string concat-then-return, comparison + unary, if/else
// (both arms taken), no-else fall-through, while-counter sum,
// early-return short-circuit inside while, undefined-ident
// VErr, division-by-zero VErr.

func writeSelfHostVMProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "vm.fern"} {
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

func TestSelfHostVMX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostVMProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "vm.fern"))
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
		t.Errorf("fern-port vm assertion %d failed", code)
	}
}

func TestSelfHostVMArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostVMProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "vm.fern"))
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
		t.Errorf("fern-port vm assertion %d failed", code)
	}
}
