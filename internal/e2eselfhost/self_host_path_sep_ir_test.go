package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pathSepIRCases pin the `::` path separator (`Type::f(args)`, #2700) to the
// self-host IR path. Native already lexes/parses `::` (path_sep_test.go); the
// self-host lexer now normalises `::` to a `.` token, so every `.`-handling
// parser site (postfix access, qualified names) treats it identically — the
// self-host AST carries no record of the separator. These cases prove `::`
// resolves to the same associated-function / method dispatch as `.` end to end
// on the self-host compiler. Routing is pinned to "ir"; exit codes are the
// oracle. Mirrors self_host_assoc_fn_ir_test.go (the `.` form).
var pathSepIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Associated function (inherent impl) called via `::`. 3 + 4 = 7.
	{"assoc-call",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { var p: Pt = Pt::make(3, 4); return p.x + p.y; }`, 7},
	// Associated function via `::`, chaining a field read off the result. 3 + 20 = 23.
	{"assoc-chained",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { return Pt::make(3, 4).x + Pt::make(10, 20).y; }`, 23},
	// `::` and `.` are interchangeable in one program — same dispatch. 0 + 9 = 9.
	{"mixed-sep",
		`struct Pt { x: i32, y: i32 } impl Pt { function origin(): Pt { return Pt { x: 0, y: 0 }; } function sum(self: Self): i32 { return self.x + self.y; } } function main(): i32 { var p: Pt = Pt::origin(); return p.sum() + 9; }`, 9},
	// Trait-impl associated function (the original #2778 form) via `::`. 42.
	{"trait-assoc",
		`trait Mk { function of(n: i32): Self; } struct Wrap { v: i32 } impl Mk for Wrap { function of(n: i32): Wrap { return Wrap { v: n }; } } function main(): i32 { var w: Wrap = Wrap::of(42); return w.v; }`, 42},
	// Enum associated constructor (nominal return) via `::`. 7.
	{"enum-ctor",
		`enum E { A(i32), B } impl E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return val(E::tag(7)); }`, 7},
}

// TestSelfHostPathSepIRX86_64 routes each `::` case through the self-hosted
// x86-64 driver and asserts the exit code, pinning routing to "ir".
func TestSelfHostPathSepIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range pathSepIRCases {
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

// TestSelfHostPathSepIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostPathSepIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host path-sep wasm IR e2e")
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

	for _, tc := range pathSepIRCases {
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
			watFile := filepath.Join(dir, "pathsep_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("path-sep wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
