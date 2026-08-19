package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// voidBareReturnIRCases pin a VOID function with an explicit bare `return;`
// (value-less) to the self-host IR path on x86-64 + wasm. parse_expr yields an
// ExprUnknown for the missing return value, which lower_expr can't lower, so the
// StmtReturn arm bailed the whole module to the legacy AST emitter — affecting
// every void helper with an early `return;` (the common guard-clause shape in the
// CLI tools). #2691 detects the ExprUnknown (only reachable in a void function;
// the checker rejects a value-less return elsewhere) and emits a dummy 0 before
// the exit dec-sweep + op_return, mirroring `return 0` (a void caller ignores the
// result). Each case is oracle-checked against the interpreter and returns <= 126.
var voidBareReturnIRCases = []struct {
	name string
	main string
}{
	// Tail bare return in a void function. main returns 42 after f.
	{"tail", `function f(x: i32): void { print("hi"); return; } function main(): i32 { f(5); return 42; }`},
	// Early bare return (guard taken) — the print is skipped. 42.
	{"early-taken", `function f(x: i32): void { if (x > 0) { return; } print("neg"); } function main(): i32 { f(5); return 42; }`},
	// Early bare return (guard NOT taken) — the print runs, then fall-through. 42.
	{"early-not-taken", `function f(x: i32): void { if (x > 0) { return; } print("neg"); } function main(): i32 { f(0 - 1); return 42; }`},
	// Bare return inside a loop (continue-like exit). 42.
	{"in-loop", `function f(n: i32): void { var i: i32 = 0; while (i < n) { if (i == 2) { return; } print("x"); i = i + 1; } } function main(): i32 { f(5); return 42; }`},
	// Two void helpers, each with a bare return, both called. 42.
	{"two-helpers", `function a(x: i32): void { if (x > 0) { return; } print("a"); } function b(x: i32): void { print("b"); return; } function main(): i32 { a(1); b(2); return 42; }`},
}

// TestSelfHostVoidBareReturnIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostVoidBareReturnIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range voidBareReturnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostVoidBareReturnIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostVoidBareReturnIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host void-bare-return wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range voidBareReturnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "void_bare_return_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("void-bare-return wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
