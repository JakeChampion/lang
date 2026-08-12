package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deriveDefaultEnumIRCases exercise `@derive(Default)` on concrete ENUMS
// through the stack-IR path. An enum defaults to its FIRST variant, each
// payload defaulted. The synthesized `default()` is an associated function
// (receiver-less `Enum.default()`) that constructs a variant; both the
// associated call and the variant construction now lower through the IR path
// (the enum-in-IR slice: enum returns are registered in struct_ret_fns, and
// the assoc-fn lowering recognises an enum target by its registered return
// type). `match` reads the variant via shape-pointer identity — the same
// representation IR `struct_make` writes — so a freshly-defaulted variant
// matches correctly.
//
// The inline `trait Default` keeps the program valid for the native compiler
// too (the self-host discards trait decls).
var deriveDefaultEnumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// First variant is a UNIT variant → bare variant value. 3.
	{"unit-first",
		`trait Default { function default(): Self; } @derive(Default) enum E { A, B(i32) } function main(): i32 { var e: E = E.default(); match (e) { A => { return 3; }, B(n) => { return n; } } }`, 3},
	// First variant has an i32 payload → defaulted to 0. 0 + 4 = 4.
	{"payload-i32",
		`trait Default { function default(): Self; } @derive(Default) enum E { Wrap(i32), Other } function main(): i32 { var e: E = E.default(); match (e) { Wrap(n) => { return n + 4; }, Other => { return 1; } } }`, 4},
	// First variant has a string payload → defaulted to "". 0 + 9 = 9.
	{"payload-string",
		`trait Default { function default(): Self; } @derive(Default) enum E { Msg(string), None } function main(): i32 { var e: E = E.default(); match (e) { Msg(s) => { return s.len() + 9; }, None => { return 1; } } }`, 9},
	// First variant has a boolean payload → defaulted to false. 6.
	{"payload-boolean",
		`trait Default { function default(): Self; } @derive(Default) enum E { Flag(boolean), Off } function main(): i32 { var e: E = E.default(); match (e) { Flag(b) => { if (b) { return 1; } return 6; }, Off => { return 2; } } }`, 6},
	// Inferred binding: `var e = E.default()` recovers the enum type from the
	// registered associated-call return type. First variant A → 7.
	{"inferred-binding",
		`trait Default { function default(): Self; } @derive(Default) enum E { A, B(i32) } function main(): i32 { var e = E.default(); match (e) { A => { return 7; }, B(n) => { return n; } } }`, 7},
}

// TestSelfHostDeriveDefaultEnumIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostDeriveDefaultEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range deriveDefaultEnumIRCases {
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

// TestSelfHostDeriveDefaultEnumIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir).
func TestSelfHostDeriveDefaultEnumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host derive-default enum wasm IR e2e")
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

	for _, tc := range deriveDefaultEnumIRCases {
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
			watFile := filepath.Join(dir, "derivedefaultenum_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("derive-default enum wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
