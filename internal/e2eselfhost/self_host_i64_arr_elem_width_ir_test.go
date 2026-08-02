package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i64ArrElemWidthIRCases pin an i32-element ARRAY index consumed in an i64
// arithmetic context (`s64 + a[i]`, the canonical numeric reduction summing an
// i32[] into an i64 accumulator) to the self-host IR path on x86-64 + wasm.
// lower_i64's ExprIndex arm previously lowered only i64[]/u64[] (8-byte)
// elements and bailed every other array source via `return s.fail()`, dropping
// the whole module to the legacy AST emitter. #2691 widens it: a plain 32-bit
// element array (new arr_index_is_i32_scalar — an i32[] or u32[] ident slot, not
// i64[]/f64[]/string[]/T[][]/closure[]) has its element lowered via lower_expr
// (arr_get) and sign/zero-extended to i64 (op_int_extend; the checker forbids
// i64 + u32, so a plain i32[] element here is signed). Each case narrows the i64
// result with `as i32` so the wasm _start exit code is a valid i32 in [0,126),
// and is oracle-checked against the interpreter.
var i64ArrElemWidthIRCases = []struct {
	name string
	main string
}{
	// Sum an i32[] into an i64 accumulator across a for-range. 10+20+30 = 60.
	{"sum-loop", `function main(): i32 { var a: i32[] = [10,20,30]; var s: i64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	// Direct i64 + i32 element. 36 + a[1] = 36 + 6 = 42.
	{"direct-elem", `function main(): i32 { var a: i32[] = [5,6,7]; var s: i64 = 36; return (s + a[1]) as i32; }`},
	// Sign-extension: NEGATIVE i32 elements must sign-extend. 50 + (-5) + (-3) = 42.
	{"neg-elems", `function main(): i32 { var a: i32[] = [-5, -3]; var s: i64 = 50; return (s + a[0] + a[1]) as i32; }`},
	// Element used in a multiply inside the i64 context. 0 + 6*7 = 42.
	{"elem-mul", `function main(): i32 { var a: i32[] = [6, 7]; var s: i64 = 0; return (s + a[0] * a[1]) as i32; }`},
	// Regression: an i64[] element source still uses the 8-byte read. 10 + 32 = 42.
	{"i64arr-keep", `function main(): i32 { var a: i64[] = [10, 32]; var s: i64 = 0; return (s + a[0] + a[1]) as i32; }`},
}

// TestSelfHostI64ArrElemWidthIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostI64ArrElemWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range i64ArrElemWidthIRCases {
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

// TestSelfHostI64ArrElemWidthIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostI64ArrElemWidthIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-arr-elem-width wasm IR e2e")
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

	for _, tc := range i64ArrElemWidthIRCases {
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
			watFile := filepath.Join(dir, "i64_arr_elem_width_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("i64-arr-elem-width wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
