package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fnParamRetStructIRCases pin calling a fn-typed PARAM whose declared return
// type is a STRUCT, then reading a field / calling a method on the result
// (`function call(g: () => P): i32 { return g().x; }`). The parser coarsens a
// `() => P` param to the flat tag "fn", discarding the return spelling, so
// `g().field` could not resolve P's fields and the module bailed to the AST
// path (which also miscompiled some shapes). The fix (#3640) preserves the fn
// return type on ParamDecl.fn_ret past the coarsening, records it per fn-param
// in the fn_param_sigs registry (a third "|rets" segment, invisible to the
// existing flag readers), and resolves `g().field`/`g().method()` through it in
// expr_struct_type. A fn-param returning a scalar/array is unaffected (the
// resolver guards on a real struct/enum).
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; every result stays <= 120 (the wasm exit-code clamp).
var fnParamRetStructIRCases = []struct {
	name string
	main string
}{
	// the headline shape: 0-arg fn-param returning a struct, field read → 4.
	{"field", `struct P { x: i32 } function call(g: () => P): i32 { return g().x; } function mk(): P { return P { x: 4 }; } function main(): i32 { return call(mk); }`},
	// a 1-arg fn-param returning a 2-field struct, both fields read → 3 + 10 = 13.
	{"two-field-one-arg", `struct P { x: i32, y: i32 } function call(g: (i32) => P): i32 { return g(3).x + g(3).y; } function mk(n: i32): P { return P { x: n, y: 10 }; } function main(): i32 { return call(mk); }`},
	// a METHOD call on the struct result (`g().get()`) → 8.
	{"method", `struct P { x: i32 } function (p: P) get(): i32 { return p.x; } function call(g: () => P): i32 { return g().get(); } function mk(): P { return P { x: 8 }; } function main(): i32 { return call(mk); }`},
	// bind the result to a local first, then read it (`var p = g(); p.x`) → 6.
	{"via-local", `struct P { x: i32 } function call(g: () => P): i32 { var p = g(); return p.x; } function mk(): P { return P { x: 6 }; } function main(): i32 { return call(mk); }`},
	// Regression: a fn-param returning a SCALAR (not a struct) stays correct → 6.
	{"scalar-ret", `function call(g: () => i32): i32 { return g() + 1; } function mk(): i32 { return 5; } function main(): i32 { return call(mk); }`},
}

// TestSelfHostFnParamRetStructIRX86_64 routes each case through the self-host
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostFnParamRetStructIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range fnParamRetStructIRCases {
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

// TestSelfHostFnParamRetStructIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFnParamRetStructIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-param-ret-struct wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range fnParamRetStructIRCases {
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
			watFile := filepath.Join(dir, "fnparam_ret_struct_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("fn-param-ret-struct wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
