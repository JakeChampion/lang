package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// enumFreshMethodIRCases exercise an enum method called DIRECTLY on a freshly
// constructed variant — a payload variant `Has(5).m()` or a bare unit variant
// `Nil.m()` — through the stack-IR path. Previously such calls bailed to the
// AST emitter: a fresh variant isn't a typed local, so the receiver's enum
// couldn't be recovered for `<Enum>.<method>` dispatch (and a unit variant was
// mis-read as an associated-function TYPE target). The parser now records each
// variant's owning enum on its desugared StructDecl (`enum_owner`), and
// irlower's `expr_enum_type` recovers it. Exit codes are the oracle.
var enumFreshMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Payload variant receiver. Has(5) → 5 + 1 = 6.
	{"payload-receiver",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Has(5).tagval(); }`, 6},
	// Unit variant receiver (a bare ident, not a type). Nil → 99.
	{"unit-receiver",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Nil.tagval(); }`, 99},
	// Both forms in one expression. 6 + 99 = 105.
	{"payload-and-unit",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Has(5).tagval() + Nil.tagval(); }`, 105},
	// A method taking an argument, dispatched on a fresh variant. 5 + 10 = 15.
	{"method-with-arg",
		`enum E { Has(i32), Nil } function (self: E) addto(k: i32): i32 { match (self) { Has(n) => { return n + k; }, Nil => { return k; } } } function main(): i32 { return Has(5).addto(10); }`, 15},
	// Derived Eq on an enum, all comparisons on fresh variants (the
	// trait-derive-enum-eq shape). 1 + 2 + 4 + 8 = 15.
	{"derived-eq",
		`trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) enum Opt { Has(i32), Nil } function main(): i32 { var r: i32 = 0; if (Has(5).eq(Has(5))) { r = r + 1; } if (!Has(5).eq(Has(6))) { r = r + 2; } if (!Has(5).eq(Nil)) { r = r + 4; } if (Nil.eq(Nil)) { r = r + 8; } return r; }`, 15},
}

// TestSelfHostEnumFreshMethodIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostEnumFreshMethodIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range enumFreshMethodIRCases {
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

// TestSelfHostEnumFreshMethodIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir).
func TestSelfHostEnumFreshMethodIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host enum-fresh-method wasm IR e2e")
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

	for _, tc := range enumFreshMethodIRCases {
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
			watFile := filepath.Join(dir, "enumfresh_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("enum-fresh-method wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
