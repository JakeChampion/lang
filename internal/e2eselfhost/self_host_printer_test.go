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

// Fifth self-host milestone after the lexer (#609), parser (#611 /
// #617), checker (#619), and interp (#623). `printer.fern` is a
// tree-to-source emitter: walks a parser.Module and emits a
// re-parseable string. Validates the round-trip property —
// parse → print → parse → same AST shape — so printer bugs
// can't silently drop or mangle nodes.
//
// Covers every Expr / Stmt variant the parser currently handles:
// numbers (with suffix), idents, strings (with `\n` / `\"` / `\\`
// re-escape), bools, binary / unary / call / array / index /
// unknown; var / return / expr / if / while / assign / unknown
// stmts; function decls; full Module top-level.

func writeSelfHostPrinterProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "printer.fern")
	return dir
}

func TestSelfHostPrinterX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostPrinterProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "printer.fern"))
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
		t.Errorf("fern-port printer assertion %d failed", code)
	}
}

func TestSelfHostPrinterArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostPrinterProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "printer.fern"))
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
		t.Errorf("fern-port printer assertion %d failed", code)
	}
}
