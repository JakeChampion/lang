package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// clockIRCases exercise monotonic_ns() / now_unix_ms() — 0-arg i64 clock
// readings — through the IR path. Each lowers to a dedicated IR op
// (op_monotonic_ns / op_now_unix_ms) that the x86-64 / arm64 backends emit as a
// call into the same __fern_* clock helper the AST path already uses, so a
// timing program stays IR-eligible instead of bailing the whole module to the
// AST emitter. The monotonic clock is non-decreasing, and a fresh wall-clock
// reading is too over the handful of instructions between the two calls, so
// `b >= a` ⇒ exit 7 (1 would mean time went backwards).
var clockIRCases = []struct {
	name, src, helper string
}{
	{"monotonic", `function main(): i32 { var a: i64 = monotonic_ns(); var b: i64 = monotonic_ns(); if (b >= a) { return 7; } return 1; }`, "__fern_monotonic_ns"},
	{"now_unix", `function main(): i32 { var a: i64 = now_unix_ms(); var b: i64 = now_unix_ms(); if (b >= a) { return 7; } return 1; }`, "__fern_now_unix_ms"},
	{"now_ns", `function main(): i32 { var a: i64 = now_ns(); var b: i64 = now_ns(); if (b >= a) { return 7; } return 1; }`, "__fern_now_ns"},
}

// TestSelfHostClockIRX86_64 proves each case (a) routes through the IR path
// (asm_pathprobe_run prints "ir", not "ast") and (b) compiles + runs to exit 7
// through the production x86-64 driver (asm_run, IR default-on), with the
// emitted asm calling the clock helper.
func TestSelfHostClockIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range clockIRCases {
		t.Run(tc.name, func(t *testing.T) {
			// (a) path probe: the module must be fully IR-eligible.
			probe := runCapture(t, gcc, runner, probeBin, []byte(tc.src))
			if strings.TrimSpace(string(probe)) != "ir" {
				t.Fatalf("%s: path probe = %q, want \"ir\" (clock builtin bailed the module to the AST path)", tc.name, probe)
			}
			// (b) compile + run through the IR-default driver. Both native IR
			// paths host the clock helpers as Fern runtime functions (#2649),
			// so the op handler calls the stack-ABI __fn_-prefixed symbol.
			// wasm is the exception: it keeps `call $__fern_<clock>` over the
			// preview1 clock_time_get import.
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fn_"+tc.helper)) {
				t.Fatalf("%s: emitted asm has no `call __fn_%s`", tc.name, tc.helper)
			}
			progBin := buildBin(t, gcc, dir, "clk_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 7 {
				t.Errorf("%s: exit %d, want 7", tc.name, code)
			}
		})
	}
}

// TestSelfHostClockIRWasm runs the same cases through the wasm IR backend under
// wasmtime. Each clock op now emits `call $__fern_<clock>` (wasm_ir), and
// wasm_ir_run pulls in the preview1 wasi clock_time_get import + the clock_funcs
// helpers (the same runtime the wasm AST path uses) when the module reads any
// clock — so a timing module is wasm-IR-eligible instead of bailing to the AST
// emitter. The non-decreasing-clock contract again gives exit 7.
func TestSelfHostClockIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host clock wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range clockIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			// IR routing: the clock op must lower to `call $__fern_<clock>`
			// (wasm_ir's op arm), not bail to the AST emitter.
			if !bytes.Contains(wat, []byte("call $"+tc.helper)) {
				t.Fatalf("%s: emitted wat has no `call $%s` — clock builtin did not lower through the wasm IR path", tc.name, tc.helper)
			}
			watFile := filepath.Join(dir, "clk_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != 7 {
				t.Errorf("clock wasm IR %q: exit %d, want 7", tc.name, code)
			}
		})
	}
}

// TestSelfHostClockIRArm64 runs the same cases through the arm64 IR backend
// under qemu (CI-gated). arm64 hosts the clocks as Fern runtime functions too
// (#2649), so the op handler calls the stack-ABI `bl __fn___fern_<clock>` —
// note that `bl __fern_<clock>`, what this asserted before the migration, is
// NOT a substring of it: the `__fn___` prefix sits between. That is what makes
// this a t.Fatalf that silently skipped the qemu run rather than a mismatch
// something downstream would have caught.
func TestSelfHostClockIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range clockIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fn_"+tc.helper)) {
				t.Fatalf("%s: emitted asm has no `bl __fn_%s` — clock builtin did not lower through the arm64 IR path", tc.name, tc.helper)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "clk_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != 7 {
				t.Errorf("clock arm64 IR %q: exit %d, want 7", tc.name, code)
			}
		})
	}
}
