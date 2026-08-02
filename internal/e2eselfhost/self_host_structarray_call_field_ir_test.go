package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// structArrayCallFieldIRCases pin field access on a struct ELEMENT indexed directly
// from a function that returns an array of structs (`mk()[i].field`) to the
// self-host IR path on x86-64 + wasm. The neighbours already lowered — binding the
// array first (`var a = mk(); a[i].field`) and indexing without a field
// (`var p = mk()[i]`) both route "ir" — but the inline `mk()[i].field` shape bailed
// to the legacy AST emitter: expr_struct_type's ExprIndex arm had no ExprCall case,
// so it couldn't recover the element type for `mk()[i]` and the field read failed.
// #2691 adds that case (struct_ret_type already records the P[]-return element type
// "P", #3035). Each case is oracle-checked against the interpreter and returns
// <= 126. Mirrors self_host_nested_array_ir_test.go.
var structArrayCallFieldIRCases = []struct {
	name string
	main string
}{
	// Field reads off two elements indexed from the call. 3 + 2 = 5.
	{"call-idx-field-sum", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[1].x + mk()[0].y; }`},
	// First element's x. 1.
	{"call-idx-field-x", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[0].x; }`},
	// Second element's y. 4.
	{"call-idx-field-y", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[1].y; }`},
	// Regression: binding the array first (already lowered) stays on the IR path. 5.
	{"bind-first", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { var a: P[] = mk(); return a[1].x + a[0].y; }`},
	// Regression: index-only (no field, already lowered) stays on the IR path. 3.
	{"index-only", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { var p = mk()[1]; return p.x; }`},
}

// TestSelfHostStructArrayCallFieldIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStructArrayCallFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range structArrayCallFieldIRCases {
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

// TestSelfHostStructArrayCallFieldIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStructArrayCallFieldIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host structarray-call-field wasm IR e2e")
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

	for _, tc := range structArrayCallFieldIRCases {
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
			watFile := filepath.Join(dir, "structarray_call_field_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("structarray-call-field wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
