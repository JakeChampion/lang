package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLoopReuseWasmIR proves loop-body FBIP reuse (irlower
// `lower_loop_body`) lowers correctly on the self-hosted WASM IR backend,
// including the recipient prior-release (an `if (old != donor) rc_dec` guarding
// the loop-carried box). Value-through-wasmtime is the contract; exit codes stay
// < 126 for WASI's _start range.
func TestSelfHostLoopReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host loop-reuse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Struct loop reuse fires per iteration: sum over i in 0..3 of
		// (i+(i+1)) + (i*2+3) = 40.
		{"loop-struct-reuse", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`, 40},
		// Tuple loop reuse: sum over i in 0..3 of (i+(i+1)) + (i+3) = 34.
		{"loop-tuple-reuse", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`, 34},
		// Donor-live control (reuse suppressed): value stays correct across the loop.
		{"loop-tuple-donor-live", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var b: (i32, i32) = (i, 3); sum = sum + a.0 + a.1 + b.0 + b.1; i = i + 1; } return sum; }`, 34},
		// Higher iteration count: the prior-release must free each loop-carried box
		// (a double-free would trap). 2000 iters, value kept small (sum mod 100 = 0).
		{"loop-struct-churn", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 2000) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 100; i = i + 1; } return sum; }`, 0},
	}
	for _, tc := range cases {
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
			if !strings.Contains(string(wat), "$__fern_str_box") {
				t.Errorf("%q did not reach the IR box path (no box in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("loop reuse wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
