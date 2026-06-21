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
// The `.append`-built form (`fns = fns.append(() => n)`) is now covered too
// (#3556): an EMPTY closure-array literal `var fns: (() => i32)[] = []` leaves
// the slot a generic array (no closure elements to infer from at the decl), so
// the later `fns[i]()` dispatched a closure box as a plain fn pointer and
// segfaulted while `all_eligible` wrongly admitted it. The fix marks the slot a
// closure array at the FIRST closure `.append` (when the appended value is a
// `__mkclo$…` env box / closure-returning call / closure local), so the indexed
// call dispatches env-first. A bare NAMED-function value (`[f]` / `append(f)`)
// is left a plain fn pointer — its broader mis-lowering is the separate #3574
// (even `var g: () => i32 = f; g()` segfaults).
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
	// #3556: append a capturing closure to an EMPTY `(() => i32)[]`, then call it.
	{"append-empty", `function main(): i32 { var n = 4; var fns: (() => i32)[] = []; fns = fns.append(() => n); return fns[0](); }`, 4},
	// append two closures, call both: 10 + 20 = 30.
	{"append-two", `function main(): i32 { var a = 10; var b = 20; var fns: (() => i32)[] = []; fns = fns.append(() => a); fns = fns.append(() => b); return fns[0]() + fns[1](); }`, 30},
	// append onto a NON-empty literal, then call the appended element.
	{"append-after-literal", `function main(): i32 { var n = 4; var fns: (() => i32)[] = [() => n]; fns = fns.append(() => n + 1); return fns[1](); }`, 5},
	// append three, then sum them via a `for` loop over the array: 1+2+3 = 6.
	{"append-loop", `function main(): i32 { var a = 1; var b = 2; var c = 3; var fns: (() => i32)[] = []; fns = fns.append(() => a); fns = fns.append(() => b); fns = fns.append(() => c); var s = 0; for f in fns { s = s + f(); } return s; }`, 6},
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
