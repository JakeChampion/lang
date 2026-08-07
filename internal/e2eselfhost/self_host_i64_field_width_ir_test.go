package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i64FieldWidthIRCases pin an i32 STRUCT FIELD or i32 TUPLE ELEMENT consumed in
// an i64 arithmetic context (`s64 + p.x`, `s64 + t.0`) to the self-host IR path
// on x86-64 + wasm. lower_i64's ExprFieldAccess arm must not lower only i64
// struct fields / i64 tuple elements (8-byte struct_get_i64 / tuple_get_w) and
// bail every other field via `return s.fail()`, dropping the whole module to
// the legacy AST emitter. #2691 widens it: an i32/u32 struct field or tuple
// element has its value lowered via lower_expr and sign/zero-extended to i64
// (op_int_extend). The checker forbids i64 + u32 (E009), so a plain i32 member
// here is signed; the u32 flag stays defensive. This is the struct/tuple sibling
// of the i32-ident and i32-array-element widenings. Each case narrows the i64
// result with `as i32` (valid wasm exit code in [0,126)) and is oracle-checked.
var i64FieldWidthIRCases = []struct {
	name string
	main string
}{
	// i64 local + i32 struct field. 30 + 12 = 42.
	{"struct-field", `struct P { x: i32 } function main(): i32 { var p: P = P { x: 12 }; var s: i64 = 30; return (s + p.x) as i32; }`},
	// Sign-extension: a NEGATIVE i32 field must sign-extend. 50 + (-8) = 42.
	{"struct-neg", `struct P { x: i32 } function main(): i32 { var p: P = P { x: -8 }; var s: i64 = 50; return (s + p.x) as i32; }`},
	// Two i32 fields summed into i64. 20 + 22 = 42.
	{"struct-two", `struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 20, y: 22 }; var s: i64 = 0; return (s + p.x + p.y) as i32; }`},
	// i64 local + i32 tuple element. 30 + 12 = 42.
	{"tuple-elem", `function main(): i32 { var t: (i32, i32) = (12, 7); var s: i64 = 30; return (s + t.0) as i32; }`},
	// Sign-extension on a tuple element. 50 + (-8) = 42.
	{"tuple-neg", `function main(): i32 { var t: (i32, i32) = (-8, 1); var s: i64 = 50; return (s + t.0) as i32; }`},
	// Regression: an i64 struct field still uses the 8-byte read. 0 + 42 = 42.
	{"struct-i64-keep", `struct P { x: i64 } function main(): i32 { var p: P = P { x: 42 }; var s: i64 = 0; return (s + p.x) as i32; }`},
	// Regression: an i64 tuple element still uses the 8-byte read. 0 + 42 = 42.
	{"tuple-i64-keep", `function main(): i32 { var t: (i64, i32) = (42, 1); var s: i64 = 0; return (s + t.0) as i32; }`},
}

// TestSelfHostI64FieldWidthIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostI64FieldWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range i64FieldWidthIRCases {
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

// TestSelfHostI64FieldWidthIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostI64FieldWidthIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-field-width wasm IR e2e")
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

	for _, tc := range i64FieldWidthIRCases {
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
			watFile := filepath.Join(dir, "i64_field_width_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("i64-field-width wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
