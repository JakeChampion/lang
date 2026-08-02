package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// callOnCallIRCases exercise calling the RESULT of a call — `mk()(args)`, where
// `mk` returns a function value — through the self-host IR path on x86-64 + wasm.
//
// Binding first (`var g = mk(); g(args)`) and calling a fn-pointer array element
// (`fs[i](args)`) already lowered; only the inline call-on-call-result form
// bailed, because the ExprCall callee dispatch had no arm for an `ExprCall`
// callee. The fix lowers the args, then the callee call (its returned fn pointer
// on TOS), then call_indirect — the same shape as the array-element / tuple-
// element fn-value calls. A callee that returns a CAPTURING lambda (a
// closure-box-returning fn, tracked in closure_fns) needs the env-passing form
// and still bails to AST (guarded here only indirectly: those programs route
// AST, so they are not in this IR-pinned set).
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a value <= 126 (wasmtime exit-code truncation, cf. #2908).
var callOnCallIRCases = []struct {
	name string
	main string
}{
	// mk() returns `b -> b+1`; calling it inline with 4 = 5.
	{"nocap", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b + 1; }; }
function main(): i32 { return mk()(4); }`},
	// Result fed into arithmetic: (5*3) + 1 = 16.
	{"in-expr", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b * 3; }; }
function main(): i32 { return mk()(5) + 1; }`},
	// Two-argument returned lambda, called inline: 5 + 6 = 11.
	{"two-arg", `function mk(): (i32, i32) => i32 { return function(a: i32, b: i32): i32 { return a + b; }; }
function main(): i32 { return mk()(5, 6); }`},
	// Returned lambda called inline twice: (4+1) + (10+1) = 16.
	{"twice", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b + 1; }; }
function main(): i32 { return mk()(4) + mk()(10); }`},
	// Regression: binding the result first still lowers (4 + 1 = 5).
	{"bind-regress", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b + 1; }; }
function main(): i32 { var g = mk(); return g(4); }`},
	// Regression: calling a fn-pointer array element still lowers (4 + 1 = 5).
	{"fnarr-regress", `function inc(b: i32): i32 { return b + 1; }
function main(): i32 { var fs: ((i32) => i32)[] = [inc]; return fs[0](4); }`},
}

// TestSelfHostCallOnCallIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostCallOnCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range callOnCallIRCases {
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

// TestSelfHostCallOnCallIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCallOnCallIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host call-on-call wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range callOnCallIRCases {
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
			watFile := filepath.Join(dir, "coc_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("call-on-call wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
