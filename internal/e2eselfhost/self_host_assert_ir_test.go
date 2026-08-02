package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4416: the `assert(cond)` / `assert(cond, msg)` builtin desugars in the
// self-host parser (mirroring the native parser) to
// `if (!cond) { eprint("assertion failed[: msg]"); exit(1); }`, so it lowers
// on the self-host IR path with no dedicated codegen — the constructs it uses
// (`!`, string `+`, `eprint`, `exit`, `if`) all already lower there. Each
// program is compiled by the self-hosted compiler and the resulting binary
// run: a passing assert falls through to the program's `return N` (exit N), a
// failing one aborts with `exit(1)`. Both are observable because the self-host
// driver produces a real executable (x86-64) / a `wasmtime run` CLI module
// (wasm), unlike the native wasmbin `--invoke main` harness.
var assertIRCases = []struct {
	name string
	main string
	want int
}{
	// Two passing asserts, then a normal return → exit 7.
	{"pass", `function main(): i32 { assert(5 > 0, "pos"); assert(1 < 2); return 7; }`, 7},
	// A failing assert with a message aborts before the return → exit 1.
	{"fail-msg", `function main(): i32 { assert(0 > 1, "boom"); return 42; }`, 1},
	// A failing assert with no message → exit 1.
	{"fail-no-msg", `function main(): i32 { assert(1 > 2); return 42; }`, 1},
	// The condition is a runtime call, evaluated once; passes → exit 3.
	{"runtime-cond", `function pos(x: i32): boolean { return x > 0; }
function main(): i32 { assert(pos(9), "pos"); return 3; }`, 3},
}

// TestSelfHostAssertIRX86_64 routes each case through the self-hosted x86-64
// IR driver and runs the produced binary, oracle-checking its exit code.
func TestSelfHostAssertIRX86_64(t *testing.T) {
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

	for _, tc := range assertIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostAssertIRWasm runs the same cases through the wasm IR backend.
// `wasmtime run` uses the CLI convention (main's return → process exit code,
// and `exit(1)` → process exit code), so both the passing and aborting cases
// are observable here.
func TestSelfHostAssertIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host assert wasm IR e2e")
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

	for _, tc := range assertIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "assert_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("assert wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
