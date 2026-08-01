package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRPerModuleLink exercises the emit half of the per-module
// compilation epic (#3451 step 1 / #3453, step 3 / #3455): a multi-module
// program emitted as SEPARATE translation units that link into one binary.
//
// asm_ir_run `-ir-unit entry|lib` drives asm_ir.emit_module_ir_unit:
//   - lib   → emit_module_ir_unit(is_entry=false, globl_defs=true): a library
//     module exposing only its `.globl __fn_*` definitions, with no
//     `_start` and no runtime.
//   - entry → emit_module_ir_unit(is_entry=true, globl_defs=true): the program
//     entry — `_start` (→ call __fn_main + exit) plus the shared
//     runtime, defs exported.
//
// The entry module A calls `bfoo`, defined in library module B. In A's unit
// that call is `call __fn_bfoo` against an UNRESOLVED extern; B's unit defines
// and exports `__fn_bfoo`. Assembling + linking the two .s files resolves the
// extern, and the binary computes 6*7 = 42. This is exactly #3453's emit
// acceptance: "A's output references __fn_bfoo as an extern, and the linked
// binary runs correctly," with eligibility's program-wide known set (the
// `-ir-extern bfoo` already pinned by TestSelfHostIRExternProbe) admitting the
// cross-module call.
func TestSelfHostIRPerModuleLink(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	// emit runs the driver over prog (on stdin) with the given args, returning
	// stdout (the emitted .s).
	emit := func(t *testing.T, prog string, args ...string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit failed (args %v) for %q: %v", args, prog, err)
		}
		return string(out)
	}

	// Library module B: defines bfoo. Emitted as a non-entry unit.
	libAsm := emit(t, "function bfoo(x: i32): i32 { return x * 7; }", "-ir-unit", "lib")
	if !strings.Contains(libAsm, ".globl __fn_bfoo") {
		t.Fatalf("lib unit did not export __fn_bfoo as .globl\n--- lib.s ---\n%s", libAsm)
	}
	if strings.Contains(libAsm, "_start:") {
		t.Fatalf("lib (non-entry) unit must not emit _start\n--- lib.s ---\n%s", libAsm)
	}

	// Entry module A: calls the imported bfoo (extern), defines main.
	entryAsm := emit(t, "function main(): i32 { return bfoo(6); }", "-ir-unit", "entry", "-ir-extern", "bfoo")
	if !strings.Contains(entryAsm, "call __fn_bfoo") {
		t.Fatalf("entry unit did not reference __fn_bfoo as an extern call\n--- entry.s ---\n%s", entryAsm)
	}
	if !strings.Contains(entryAsm, "_start:") {
		t.Fatalf("entry unit must emit _start\n--- entry.s ---\n%s", entryAsm)
	}

	entryPath := filepath.Join(dir, "pm_entry.s")
	libPath := filepath.Join(dir, "pm_lib.s")
	binPath := filepath.Join(dir, "pm_prog")
	if err := os.WriteFile(entryPath, []byte(entryAsm), 0o644); err != nil {
		t.Fatalf("write entry.s: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libAsm), 0o644); err != nil {
		t.Fatalf("write lib.s: %v", err)
	}
	// Link the two separately-emitted units into one binary — the cross-module
	// __fn_bfoo extern is resolved here.
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", entryPath, libPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link two units failed: %v\n%s", err, out)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("linked per-module binary exit = %d, want 42 (6*7)", code)
	}
}
