package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedTupleRetIRCases extend nested-tuple support to the RETURN/PARAM positions:
// a function whose return type or a parameter type is a nested tuple
// (`(i32, (i32, i32))`) now lowers on the IR path. Construction/access landed in
// the prior nested-tuple change; this widens the gate `tuple_elems_lowerable` to
// (a) split element tags depth-aware and (b) admit a nested-tuple element by
// recursing — the same leak-only-pointer treatment a struct/Option element gets.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var nestedTupleRetIRCases = []struct {
	name string
	main string
}{
	// Return a right-nested tuple, read the inner element.
	{"ret-right-nest", "function f(): (i32, (i32, i32)) { return (1, (2, 3)); }\nfunction main(): i32 { var t = f(); return t.1.1; }"},
	// Return + sum across the nesting boundary.
	{"ret-sum-across", "function f(): (i32, (i32, i32)) { return (1, (2, 3)); }\nfunction main(): i32 { var t = f(); return t.0 + t.1.0 + t.1.1; }"},
	// Left-nested return with a string sibling (both pointer elements).
	{"ret-left-nest-str", "function f(): ((i32, i32), string) { return ((4, 5), \"ab\"); }\nfunction main(): i32 { var t = f(); return t.0.0 + t.0.1 + t.1.len(); }"},
	// A nested tuple in PARAM position.
	{"param-nested", "function f(t: (i32, (i32, i32))): i32 { return t.0 + t.1.1; }\nfunction main(): i32 { return f((1, (2, 3))); }"},
	// Flat-tuple return regression (must stay on the IR path).
	{"ret-flat", "function f(): (i32, i32) { return (3, 4); }\nfunction main(): i32 { var t = f(); return t.0 + t.1; }"},
}

// TestSelfHostNestedTupleRetIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedTupleRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedTupleRetIRCases {
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

// TestSelfHostNestedTupleRetIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostNestedTupleRetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-tuple-return wasm IR e2e")
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

	for _, tc := range nestedTupleRetIRCases {
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
			watFile := filepath.Join(dir, "nested_tuple_ret_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested-tuple-return wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
