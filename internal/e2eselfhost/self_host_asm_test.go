package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Tenth self-host milestone — first true codegen layer.
// asm.fern walks a parser.Module and emits AT&T x86_64 assembly
// text for `return <i32-arithmetic>;` programs. The emitted asm
// is assemble-able with `gcc -nostdlib`.
//
// Lowering shape — a textual stack machine. Each expr leaves
// its result on the rt-stack via push %rax. Statement
// `return <expr>;` pops into %rdi and issues sys_exit_group
// (Linux syscall 60).
//
// Validation main() runs eight sub-checks: `return 42;` →
// movq $42 + pushq + popq %rdi + movq $60 + syscall +
// .globl _start; arithmetic forms emit addq / subq / imulq;
// division + modulo emit cqto + idivq (with movq %rdx, %rax
// for modulo); unary `-` emits negq; nested `(2+3)*4` emits
// both addq and imulq in order.

func TestSelfHostAsmX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	// Build through the shared cached path (buildSelfHostBin), NOT a
	// hand-rolled modload+emit+gcc: the cached path releases the emit's
	// dead spans (debug.FreeOSMemory) before spawning the assembler and
	// caches the linked binary, where the inline build held the multi-GB
	// emit residue in the test process while `as` spiked on asm.fern's .s
	// — past the 16 GB CI runners' RAM, OOM-killing the runner agent
	// ("The runner has received a shutdown signal", twice in a row on
	// #5046's shard1). Same conversion as TestSelfHostCrossValidation.
	binPath := buildSelfHostBin(t, gcc, dir, "asm.fern", "prog")
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port asm assertion %d failed", code)
	}
}

func TestSelfHostAsmArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "asm.fern"))
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
		t.Errorf("fern-port asm assertion %d failed", code)
	}
}
