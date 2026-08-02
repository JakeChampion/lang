package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// namedArgsIRCases exercise NAMED ARGUMENTS — `f(c = 9)`, `g(c = 3, a = 1)` —
// through the self-host stack-IR path (#2701). The parser encodes each
// `name = value` argument as a synthetic marker; fill_default_args_module
// reorders the markers + leading positionals into declared-parameter order and
// fills omitted defaults, exactly like the native `internal/defaultargs` pass
// that runs at the start of the Go checker. After resolution the call is an
// ordinary positional call, so the existing default-arg IR lowering handles it
// unchanged — the cases below all route "ir".
//
// Each value is pinned ≤ 255 and oracle-checked against `fern -interp`. They
// cover: a trailing named arg after a positional, a fully-reordered all-named
// call, a named arg that SKIPS a defaulted middle parameter, a named arg that
// OVERRIDES a default, and a string-typed parameter bound out of order.
var namedArgsIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Trailing named arg; `b` keeps its default. f(7,2,9)=729, -700 => 29.
	{"trailing-named",
		`function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(7, c = 9) - 700; }`, 29},
	// All-named, fully reordered: a=1,b=2,c=3 => 123.
	{"reorder-all-named",
		`function f(a: i32, b: i32, c: i32): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(c = 3, a = 1, b = 2); }`, 123},
	// Named arg skips a defaulted middle param: g(3, h=5(default), b=9) => 3+50+9 = 62.
	{"skip-defaulted-middle",
		`function g(p: i32, h: i32 = 5, b: i32 = 7): i32 { return p + h * 10 + b; } function main(): i32 { return g(3, b = 9); }`, 62},
	// Named arg overrides a default: g(1, h=2, b=7(default)) => 1+20+7 = 28.
	{"override-default",
		`function g(p: i32, h: i32 = 5, b: i32 = 7): i32 { return p + h * 10 + b; } function main(): i32 { return g(1, h = 2); }`, 28},
	// String-typed param bound by name, out of order: "abc".len()+5 = 8.
	{"string-param-named",
		`function mk(prefix: string, n: i32 = 0): i32 { return prefix.len() + n; } function main(): i32 { return mk(n = 5, prefix = "abc"); }`, 8},
}

// TestSelfHostNamedArgsIRX86_64 routes each case through the self-hosted x86-64
// driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostNamedArgsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range namedArgsIRCases {
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

// TestSelfHostNamedArgsIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir).
func TestSelfHostNamedArgsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host named-args wasm IR e2e")
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

	for _, tc := range namedArgsIRCases {
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
			watFile := filepath.Join(dir, "namedargs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("named-args wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
