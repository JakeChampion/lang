package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tcoIRCases pin tail-call optimisation on the self-host IR path (#4328). A
// self-recursive call in tail position (`return f(args)`) must reuse the
// current activation via a loop, so recursion that would blow an 8 MB stack
// (~1M frames) completes in O(1) stack instead of SIGSEGV-ing. Non-tail
// recursion (factorial) is a control: it must NOT be rewritten and must still
// give the right answer. Expectations are the native-compiler results (native
// has TCO, so it is the oracle for the deep cases). Each case is pinned to the
// "ir" path on x86.
var tcoIRCases = []struct {
	name string
	src  string
	want int
}{
	// Deep self-tail recursion (~1M frames). Without TCO the emitted binary
	// SIGSEGVs (exit 139); with it, it loops. 1000000*1000001/2 mod 100 = 64.
	{
		"deep-tail-accumulator",
		"function sum_to(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return sum_to(n - 1, acc + n); } " +
			"function main(): i32 { return sum_to(1000000, 0) % 100; }",
		64,
	},
	// Tail call nested inside an else (depth-2 branch to the wrapper loop).
	{
		"deep-tail-in-else",
		"function f(n: i32, acc: i32): i32 { if (n == 0) { return acc; } else { return f(n - 1, acc + 1); } } " +
			"function main(): i32 { return f(500003, 0) % 100; }",
		3,
	},
	// Non-tail recursion must be left alone (n * fact(n-1) is not a tail call).
	{"non-tail-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
	// A shallow tail call still returns the right value (TCO fires but the
	// answer is unchanged).
	{"shallow-tail", "function count(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return count(n - 1, acc + 2); } function main(): i32 { return count(20, 0); }", 40},
}

// TestSelfHostTcoIRX86_64 compiles each case through the self-host x86-64 IR
// driver (pinned to "ir") and runs the emitted binary; the deep cases must not
// SIGSEGV.
func TestSelfHostTcoIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tcoIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
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
				t.Errorf("%s exited %d, want %d (139 = SIGSEGV: TCO did not fire)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTcoIRWasm runs the same cases through the wasm IR backend, where
// deep recursion without TCO exhausts wasmtime's call stack (trap) rather than
// SIGSEGV-ing. The loop rewrite is shared (lower_func), so this exercises the
// wasm loop/br emission for the TCO'd shape.
func TestSelfHostTcoIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host TCO wasm IR e2e")
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

	for _, tc := range tcoIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
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
			watFile := filepath.Join(dir, "tco_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("TCO wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
