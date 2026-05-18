package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Bootstrap-style end-to-end demo. asm_run.lang is a driver that
// uses the self-host asm emitter (asm.lang) to lower a hardcoded
// lang source to AT&T x86_64 assembly and prints the result to
// stdout. This test:
//
//   1. Compiles asm_run.lang via the production langc into an
//      x86_64 binary.
//   2. Runs that binary, capturing its stdout — which IS the
//      emitted assembly for the hardcoded source.
//   3. Writes the asm to a .s file, assembles it with gcc
//      -nostdlib, and runs the resulting binary.
//   4. Asserts the inner binary's exit code matches the
//      expected value (23) for the source program:
//        var a = 1 + 2;       // a = 3
//        var b = 4 * 5;       // b = 20
//        var c = a + b;       // c = 23
//        if (c < 100) { return c; }
//        return 0 - 1;
//
// End-to-end: lang-port asm emitter → real native binary → 23.
// Proves the asm.lang lowering produces working executables,
// not just text that matches substring asserts.

func TestSelfHostAsmRunX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	// Stage all the lang sources asm_run.lang needs to import.
	for _, name := range []string{"lexer.lang", "parser.lang", "asm.lang", "asm_run.lang"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Step 1: build asm_run via the production pipeline.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_run.lang"))
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
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}
	// Step 2: run the driver, capture its stdout (the emitted asm).
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	emittedAsm, err := cmd.Output()
	if err != nil {
		t.Fatalf("run driver: %v", err)
	}
	if len(emittedAsm) == 0 {
		t.Fatalf("driver produced no asm output")
	}
	// Step 3: write the emitted asm, assemble, run.
	innerAsm := filepath.Join(dir, "inner.s")
	innerBin := filepath.Join(dir, "inner")
	if err := os.WriteFile(innerAsm, emittedAsm, 0o644); err != nil {
		t.Fatalf("write inner asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
		t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
	}
	var inner *exec.Cmd
	if len(runner) == 0 {
		inner = exec.Command(innerBin)
	} else {
		inner = exec.Command(runner[0], append(runner[1:], innerBin)...)
	}
	_, _ = inner.CombinedOutput()
	// Step 4: assert.
	if code := inner.ProcessState.ExitCode(); code != 23 {
		t.Errorf("bootstrap demo: inner exit code = %d, want 23\n--- asm ---\n%s", code, emittedAsm)
	}
}
