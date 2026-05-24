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

// Eighth self-host milestone — bytecode disassembler. Consumes
// the Op[] produced by `vm.compile_module(mod)` and emits one
// op per line in a fixed-width tabular format. Same tree-
// walking shape as the printer (#639), only it walks bytecode
// rather than AST.
//
// Coverage: every Op variant has a mnemonic + an immediate-
// renderer that match arms exhaustively over the union.
// Validation main() runs seven sub-checks: arithmetic
// disassembles with PUSH_INT / BINARY / STORE_LOCAL / LOAD_LOCAL
// / RETURN; index column zero-pads to four digits; comparison
// emits `<` in the BINARY immediate; if/else emits both
// JUMP_IF_FALSE and JUMP; while emits the loop-jump + body-exit
// jump; unary `!` emits UNARY; string literal renders its
// quoted contents; expression-stmt emits POP.

func writeSelfHostDisasmProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "vm.fern", "disasm.fern"} {
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

func TestSelfHostDisasmX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostDisasmProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "disasm.fern"))
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
		t.Errorf("fern-port disasm assertion %d failed", code)
	}
}

func TestSelfHostDisasmArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostDisasmProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "disasm.fern"))
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
		t.Errorf("fern-port disasm assertion %d failed", code)
	}
}
