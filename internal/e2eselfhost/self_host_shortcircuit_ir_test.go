package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// shortCircuitIRCases verify proper short-circuit && / || in the stack-IR path
// when the RIGHT operand is NOT eager-safe (a call / index / division). The IR
// path's fast path lowers `&&`/`||` of trap-free operands as a bitwise `&`/`|`
// (both eagerly evaluated); a non-eager-safe RHS instead lowers to a temp-local
// + block guard so the RHS runs ONLY when the LHS doesn't short-circuit. These
// cases pin both the value AND the side-effect/trap avoidance (a divide-by-zero
// or out-of-bounds RHS must never execute when short-circuited).
var shortCircuitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// && with a method-call RHS, both true -> 1.
	{"and-method-both-true",
		`struct P { x: i32 } function (p: P) ok(): boolean { return p.x > 0; } function main(): i32 { var a: P = P { x: 1 }; var b: P = P { x: 2 }; if (a.ok() && b.ok()) { return 1; } return 0; }`, 1},
	// && short-circuits on a false LHS: the RHS divides by zero, which must
	// NOT execute (else the program traps instead of returning 7).
	{"and-shortcircuit-skips-trap-rhs",
		`function rhs(): boolean { var z: i32 = 0; return (10 / z) == 0; } function main(): i32 { var f: boolean = false; if (f && rhs()) { return 1; } return 7; }`, 7},
	// || short-circuits on a true LHS: the trapping RHS must NOT run -> 7.
	{"or-shortcircuit-skips-trap-rhs",
		`function rhs(): boolean { var z: i32 = 0; return (10 / z) == 0; } function main(): i32 { var t: boolean = true; if (t || rhs()) { return 7; } return 1; }`, 7},
	// && where the RHS IS needed (LHS true) and returns false -> else -> 9.
	{"and-rhs-false",
		`function rhs(): boolean { return false; } function main(): i32 { var t: boolean = true; if (t && rhs()) { return 1; } return 9; }`, 9},
	// || where the RHS IS needed (LHS false) and returns true -> 5.
	{"or-rhs-true",
		`function rhs(): boolean { return true; } function main(): i32 { var f: boolean = false; if (f || rhs()) { return 5; } return 1; }`, 5},
	// Field-wise derived-Eq shape: `self.x.eq(o.x) && self.y.eq(o.y)`. Equal
	// values -> 1; the AND chains two method calls. r=3 (eq true, neq false).
	{"and-chain-eq-methods",
		`trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } struct P { x: i32, y: i32 } function (p: P) same(o: P): boolean { return p.x.eq(o.x) && p.y.eq(o.y); } function main(): i32 { var a: P = P { x: 1, y: 2 }; var b: P = P { x: 1, y: 2 }; var c: P = P { x: 1, y: 9 }; var r: i32 = 0; if (a.same(b)) { r = r + 1; } if (!a.same(c)) { r = r + 2; } return r; }`, 3},
}

// TestSelfHostShortCircuitIRX86_64 runs each case through the self-hosted
// x86-64 driver (asm_run → emit_module, IR default-on) and asserts the exit
// code — exercising the IR short-circuit-control-flow path.
func TestSelfHostShortCircuitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range shortCircuitIRCases {
		t.Run(tc.name, func(t *testing.T) {
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

// TestSelfHostShortCircuitIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir): the short-circuit block/br_if shape must produce the same
// values and trap-avoidance on the stack-machine backend.
func TestSelfHostShortCircuitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host short-circuit wasm IR e2e")
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

	for _, tc := range shortCircuitIRCases {
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
			watFile := filepath.Join(dir, "sc_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("short-circuit wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
