package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
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
	runner := x86_64Runner(t)
	dir := writeSelfHostPrinterProject(t)
	binPath := buildSelfHostBinFor(t, dir, "printer.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port printer assertion %d failed", code)
	}
}

func TestSelfHostPrinterArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostPrinterProject(t)
	binPath := buildSelfHostBinFor(t, dir, "printer.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port printer assertion %d failed", code)
	}
}
