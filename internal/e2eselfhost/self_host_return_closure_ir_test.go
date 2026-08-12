package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// returnClosureIRCases exercise calling a capturing closure that is RETURNED
// from a function, directly off the call result (`mk(..)(args)`) — the inline
// call-on-call shape. Handling only a callee that returns a bare fn pointer
// (no-capture lambda) and bailing when the callee returns a CLOSURE drops the
// module to the AST path, because that lowering
// box (a capturing-lambda-returning fn). The fix dispatches env-first off the
// returned box, the same shape `var f = mk(..); f(args)` already used.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a value <= 120 (wasmtime exit-code truncation, cf. #2908).
var returnClosureIRCases = []struct {
	name string
	main string
}{
	// A curried closure: `(x) => (y) => x + y` — the inner lambda captures x.
	{"curry", `function main(): i32 { var add = (x: i32) => (y: i32) => x + y; return add(3)(4); }`},
	// A named function returning a closure that captures its PARAMETER.
	{"return-captures-param", `function mk(k: i32): (i32) => i32 { return (y: i32) => k + y; } function main(): i32 { return mk(10)(5); }`},
	// ...capturing a LOCAL declared in the outer function.
	{"return-captures-local", `function mk(): (i32) => i32 { var k = 10; return (y: i32) => k + y; } function main(): i32 { return mk()(5); }`},
	// The returned closure takes TWO args (call_indirect arity = 2 + env).
	{"two-arg", `function mk(k: i32): (i32, i32) => i32 { return (a: i32, b: i32) => k + a + b; } function main(): i32 { return mk(10)(5, 6); }`},
	// Two captures (env box [funcval, j, k]).
	{"two-captures", `function mk(j: i32, k: i32): (i32) => i32 { return (y: i32) => j + k + y; } function main(): i32 { return mk(10, 20)(3); }`},
	// Regression: returning a NO-capture lambda (bare fn pointer) still works.
	{"return-no-capture", `function mk(): (i32) => i32 { return (y: i32) => y + 1; } function main(): i32 { return mk()(41); }`},
	// Regression: the via-var form (already worked) stays correct.
	{"via-var", `function mk(k: i32): (i32) => i32 { return (y: i32) => k + y; } function main(): i32 { var f = mk(7); return f(8); }`},
}

// TestSelfHostReturnClosureIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostReturnClosureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range returnClosureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostReturnClosureIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostReturnClosureIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host return-closure wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range returnClosureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			watFile := filepath.Join(dir, "retclosure_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("return-closure wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
