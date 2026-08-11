package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// trailingCommaCases cover a trailing comma in the two list positions the
// self-host parser has got wrong: a struct LITERAL — `P { a: 1, b: 2, }` —
// and a PARAMETER list.
//
// The struct-literal half came first: without it the self-host parser bailed
// mid-literal and cascaded into a run of ExprUnknown nodes, the sole parse gap
// blocking std/test / std/fuzz / std/tcp from parsing cleanly (std/test's
// `TestRunner` literals use the form throughout).
//
// The parameter half is #6354, and it survived because this corpus did not
// reach it. Exit codes cross-checked vs the Go backend.
var trailingCommaCases = []struct {
	name string
	src  string
	exit int
}{
	{"two-field", "struct P { a: i32, b: i32 } function main(): i32 { var p = P { a: 40, b: 2, }; return p.a + p.b; }", 42},
	{"one-field", "struct Q { v: i32 } function main(): i32 { var q = Q { v: 42, }; return q.v; }", 42},
	{"no-trailing-still-works", "struct R { a: i32, b: i32 } function main(): i32 { var r = R { a: 40, b: 2 }; return r.a + r.b; }", 42},
	{"string-field-trailing", "struct S { name: string, n: i32 } function main(): i32 { var s = S { name: \"hi\", n: 40, }; return s.name.len() + s.n; }", 42},
	// PARAMETER lists, which this corpus did not cover and which were broken
	// the whole time (#6354): the loops appended parse_param's empty sentinel
	// for the token after the comma, so each of these declared one phantom
	// nameless parameter. On a register backend that is invisible — the call
	// passes one argument too few and the callee never reads the slot — so the
	// x86-64 and arm64 legs below pass on the BROKEN parser too. Only the wasm
	// leg, where a function's arity is declared and validated, actually fails.
	// They are here so all three legs pin the parse, and in the wasm leg so one
	// of them can pin the consequence.
	{"fn-params-trailing", "function add3(a: i32, b: i32, c: i32,): i32 { return a + b + c; } function main(): i32 { return add3(35, 5, 2); }", 42},
	{"arrow-lambda-params-trailing", "function main(): i32 { var twice: (i32) => i32 = (n: i32,) => n * 2; return twice(21); }", 42},
	{"fn-keyword-lambda-params-trailing", "function main(): i32 { var f: (i32) => i32 = function (a: i32,): i32 { return a + 2; }; return f(40); }", 42},
	{"two-param-lambda-trailing", "function main(): i32 { var add: (i32, i32) => i32 = (a: i32, b: i32,) => a + b; return add(40, 2); }", 42},
	{"single-param-no-trailing-regress", "function id2(a: i32): i32 { return a; } function main(): i32 { var f: (i32) => i32 = (n: i32) => n; return id2(f(42)); }", 42},
}

// TestSelfHostTrailingCommaX86_64 — trailing comma in struct literals,
// self-hosted x86-64 compiler.
func TestSelfHostTrailingCommaX86_64(t *testing.T) {
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

	for _, tc := range trailingCommaCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTrailingCommaArm64 — CI-gated arm64 counterpart. The fix
// is in the shared parser, so the arm64 emitter needed no change; this
// guards the shared path on arm64.
func TestSelfHostTrailingCommaArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range trailingCommaCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTrailingCommaWasmIR is the leg that can SEE a phantom parameter.
//
// wasm declares each function's arity in its type and the validator checks
// every call against it, so a parameter list that parsed one entry too long is
// a module that will not load — `expected i32 but nothing on stack`, at the
// call site, with no source location. The register backends pass an argument
// too few into a slot the callee never reads and carry on, which is why the
// x86-64 and arm64 legs above stayed green through #6354 and why the fixture
// corpus (which runs this same corpus through wasm) is what finally caught it.
//
// Exit codes are checked against the interpreter rather than the table's
// literal, so this cannot drift from the reference the other legs use.
func TestSelfHostTrailingCommaWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host trailing-comma wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir,
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range trailingCommaCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.exit {
				t.Fatalf("interp oracle %d disagrees with the table's %d — fix the case", want, tc.exit)
			}
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			var se bytes.Buffer
			cmd.Stderr = &se
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (invalid module — the bug):\n%s", se.String())
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d\nstderr: %s", tc.name, code, tc.exit, se.String())
			}
		})
	}
}
