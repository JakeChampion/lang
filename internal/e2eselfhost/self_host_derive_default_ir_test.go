package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deriveDefaultIRCases exercise `@derive(Default)` on concrete structs
// through the stack-IR path. The self-host parser synthesizes the derived
// `default()` as an ASSOCIATED function (receiver-less `Type.default()`),
// which lowers via the associated-function IR path (issue #2779 item 1).
// Each field gets its type's zero: i32 → 0, string → "", boolean → false.
//
// Scope (issue #2779 item 2): concrete leaf-safe structs (scalar / string /
// boolean fields). Nested-struct composition is RC-tracked → still bails,
// and enum Default is a follow-up (a safe miss, like enum Eq/Ord derive).
// The inline `trait Default` keeps the program valid for the native
// compiler too (the self-host discards trait decls).
var deriveDefaultIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Scalar + string + boolean fields all default to zero. 0 + 0 + 5 = 5.
	{"basic-scalars",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, s: string, b: boolean } function main(): i32 { var c: Cfg = Cfg.default(); return c.a + c.s.len() + 5; }`, 5},
	// Chained: read a field straight off Cfg.default(). 0 + 6 = 6.
	{"chained",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, s: string } function main(): i32 { return Cfg.default().a + Cfg.default().s.len() + 6; }`, 6},
	// Inferred binding: `var c = Cfg.default()` (no annotation) recovers the
	// struct type from the associated-call return type. 0 + 7 = 7.
	{"inferred-binding",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, b: i32 } function main(): i32 { var c = Cfg.default(); return c.a + c.b + 7; }`, 7},
	// Boolean field defaults to false. 0 + 8 = 8.
	{"boolean-default",
		`trait Default { function default(): Self; } @derive(Default) struct F { flag: boolean, x: i32 } function main(): i32 { var f: F = F.default(); if (f.flag) { return 1; } return f.x + 8; }`, 8},
	// Several i32 fields, all zero. 0 + 0 + 0 + 10 = 10.
	{"multi-i32",
		`trait Default { function default(): Self; } @derive(Default) struct M { a: i32, b: i32, c: i32 } function main(): i32 { var m: M = M.default(); return m.a + m.b + m.c + 10; }`, 10},
}

// TestSelfHostDeriveDefaultIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run → emit_module, IR default-on) and asserts the exit
// code, AND probes the routing (asm_pathprobe_run) to pin each case to the
// "ir" path.
func TestSelfHostDeriveDefaultIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range deriveDefaultIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostDeriveDefaultIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir) so `@derive(Default)` is verified on the
// stack-machine backend too, not just the register ABI.
func TestSelfHostDeriveDefaultIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host derive-default wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range deriveDefaultIRCases {
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
			watFile := filepath.Join(dir, "derivedefault_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("derive-default wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
