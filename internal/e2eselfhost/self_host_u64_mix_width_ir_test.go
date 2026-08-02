package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u64MixWidthIRCases pin a u32 scalar leaf consumed in a u64 arithmetic context
// (`s64u + u`) to the self-host IR path on x86-64 + wasm. This is the unsigned
// mirror of the i64 mixed-width family: u64 is the ONLY context an unsigned
// 32-bit leaf can appear, since the checker forbids i64 + u32 (E009). The i32
// ident widening (is_i32_scalar_slot) wrongly REJECTED a u32 scalar: some
// var-decl paths record a u32 local's type tag in local_struct_type ("u32"), and
// is_i32_scalar_slot's struct-type exclusion fired on that bogus tag, leaving
// `u64 + u32` stuck on the AST path. #2691 admits a u32 scalar (the authoritative
// is_u32_slot signal) before the heap-type exclusions, and the widen zero-extends
// it (op_int_extend(is_u32_slot)). Each case narrows the u64 result with `as i32`
// (valid wasm exit code in [0,126)) and is oracle-checked against the interpreter.
var u64MixWidthIRCases = []struct {
	name string
	main string
}{
	// u64 local + u32 local. 40 + 2 = 42.
	{"u32-ident", `function main(): i32 { var u: u32 = 2; var s: u64 = 40; return (s + u) as i32; }`},
	// u32 local accumulated into a u64 across a for-range. 7*3 = 21.
	{"u32-loop", `function main(): i32 { var s: u64 = 0; var u: u32 = 7; for i in 0..3 { s = s + u; } return s as i32; }`},
	// u32 in a multiply inside the u64 context. 0 + 6*6 = 36.
	{"u32-mul", `function main(): i32 { var u: u32 = 6; var s: u64 = 0; return (s + u * u) as i32; }`},
	// u64 + u32 struct field. 30 + 12 = 42.
	{"u32-field", `struct P { x: u32 } function main(): i32 { var p: P = P { x: 12 }; var s: u64 = 30; return (s + p.x) as i32; }`},
	// u64 + u32 tuple element. 30 + 12 = 42.
	{"u32-tuple", `function main(): i32 { var t: (u32, u32) = (12, 7); var s: u64 = 30; return (s + t.0) as i32; }`},
	// u64 + u32[] element across a reduction. 10+20+30 = 60.
	{"u32-arr", `function main(): i32 { var a: u32[] = [10,20,30]; var s: u64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	// Regression: the i64 + i32 family is unchanged by the u32 admission. 40 + 2 = 42.
	{"i64-i32-keep", `function main(): i32 { var i: i32 = 2; var s: i64 = 40; return (s + i) as i32; }`},
}

// TestSelfHostU64MixWidthIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostU64MixWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range u64MixWidthIRCases {
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

// TestSelfHostU64MixWidthIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostU64MixWidthIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u64-mix-width wasm IR e2e")
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

	for _, tc := range u64MixWidthIRCases {
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
			watFile := filepath.Join(dir, "u64_mix_width_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("u64-mix-width wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
