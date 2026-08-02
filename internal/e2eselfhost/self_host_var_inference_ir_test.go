package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// varInferenceIRCases exercise `var x = expr;` type inference (no explicit
// `: T` annotation) through the self-host IR path on x86-64 + wasm — beyond the
// i32 path, into the wider scalars (i64 / u32 / u8-wrap / f64 / f32 / bool /
// string) and the composites (tuple / struct / array / enum), plus inference
// from a function call's return type. The inferred local's type drives the
// arithmetic / dispatch that follows, so a wrong inference would either
// mis-lower or bail.
//
// This pins the wider-type half of the "var x: T = expr + type inference" audit
// row (docs/FEATURE-AUDIT.md, previously "i32 path; wider types pending"). Each
// case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 120 (wasmtime exit-code truncation, cf. #2908).
var varInferenceIRCases = []struct {
	name string
	main string
}{
	// `var n = 7 as i64` infers i64; i64 arithmetic -> 10.
	{"infer-i64", `function main(): i32 { var n = 7 as i64; var m = n + (3 as i64); return m as i32; }`},
	// `var n = 100 as u32` infers u32 -> 120 (<= wasm clamp).
	{"infer-u32", `function main(): i32 { var n = 100 as u32; var m = n + (20 as u32); return m as i32; }`},
	// u8 inference wraps mod 256: 250 + 10 -> 4.
	{"infer-u8-wrap", `function main(): i32 { var n = 250 as u8; var m = n + (10 as u8); return m as i32; }`},
	// `var x = 2.5` infers f64; f64 arithmetic -> 4.0 -> 4.
	{"infer-f64", `function main(): i32 { var x = 2.5; var y = x + 1.5; return y as i32; }`},
	// `var x = 2.5 as f32` infers f32 -> 3.
	{"infer-f32", `function main(): i32 { var x = 2.5 as f32; var y = x + (0.5 as f32); return y as i32; }`},
	// `var b = (3 > 2)` infers boolean.
	{"infer-bool", `function main(): i32 { var b = (3 > 2); if (b) { return 1; } return 0; }`},
	// `var s = "hello"` infers string.
	{"infer-string", `function main(): i32 { var s = "hello"; return s.len(); }`},
	// `var t = (3, 4)` infers a tuple (i32, i32).
	{"infer-tuple", `function main(): i32 { var t = (3, 4); return t.0 + t.1; }`},
	// `var p = P { ... }` infers the struct type P.
	{"infer-struct", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 3, y: 4 }; return p.x + p.y; }`},
	// `var a = [10, 20, 30]` infers i32[].
	{"infer-array", `function main(): i32 { var a = [10, 20, 30]; return a[1]; }`},
	// `var e = A(7)` infers the enum type E.
	{"infer-enum", `enum E { A(i32), B } function main(): i32 { var e = A(7); return match (e) { A(n) => n, B => 0 }; }`},
	// Inference from a call's return type: `var n = ret()` where ret(): i64.
	{"infer-from-call", `function ret(): i64 { return 9 as i64; } function main(): i32 { var n = ret(); return n as i32; }`},
}

// TestSelfHostVarInferenceIRX86_64 routes each inference case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostVarInferenceIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range varInferenceIRCases {
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

// TestSelfHostVarInferenceIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostVarInferenceIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host var-inference wasm IR e2e")
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

	for _, tc := range varInferenceIRCases {
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
			watFile := filepath.Join(dir, "varinfer_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("var-inference wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
