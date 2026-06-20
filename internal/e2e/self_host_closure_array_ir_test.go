package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// closureArrayIRCases pin arrays of CAPTURING closures — built, indexed, and
// CALLED through the array — on the self-host IR path (x86-64 + wasm). The
// existing call_on_call coverage has a single NON-capturing named-function value
// in a one-element array called at a constant index (`fs[0](4)`); these cases
// exercise the distinct shape: capturing lambdas (`() => n`) stored in a
// multi-element ARRAY-LITERAL `(() => i32)[]`, called via a VARIABLE index in a
// loop. That drives closure-env boxing (the captured `n` / `k`) AND dynamic
// dispatch through an array element (the indirect call target comes from a
// runtime array read), which the constant-index non-capturing case does not.
// All of it already lowers, so no compiler change — an observability pin against
// a regression to the AST fallback.
//
// NB: the `.append`-built form (`fns = fns.append(() => n)`) is deliberately NOT
// covered — it currently mis-lowers to a runtime segfault while wrongly passing
// `all_eligible` (a soundness gap, filed separately). These cases use only
// array-literal construction, which is sound.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; every result stays <= 120 (the wasm exit-code clamp,
// #2908).
var closureArrayIRCases = []struct {
	name string
	main string
	want int
}{
	// three capturing closures, summed via a loop-variable index: 3+4+6 = 13.
	{"loop-sum", `function main(): i32 { var n = 3; var fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2]; var s = 0; var i = 0; while (i < 3) { s = s + fns[i](); i = i + 1; } return s; }`, 13},
	// constant-index call of a capturing closure: () => n + 1 with n = 3.
	{"index-const", `function main(): i32 { var n = 3; var fns: (() => i32)[] = [() => n, () => n + 1]; return fns[1](); }`, 4},
	// one-arg capturing closures: (a)=>a+k and (a)=>a*k with k=2 -> 7 + 10 = 17.
	{"two-arg-cap", `function main(): i32 { var k = 2; var fns: ((i32) => i32)[] = [(a: i32) => a + k, (a: i32) => a * k]; return fns[0](5) + fns[1](5); }`, 17},
	// single capturing closure in a one-element array.
	{"single-cap", `function main(): i32 { var n = 9; var fns: (() => i32)[] = [() => n]; return fns[0](); }`, 9},
}

// TestSelfHostClosureArrayIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostClosureArrayIRX86_64(t *testing.T) {
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

	for _, tc := range closureArrayIRCases {
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

// TestSelfHostClosureArrayIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostClosureArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-array wasm IR e2e")
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

	for _, tc := range closureArrayIRCases {
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
			watFile := filepath.Join(dir, "closure_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("closure-array wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
