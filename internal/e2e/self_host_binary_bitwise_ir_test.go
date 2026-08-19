package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binaryBitwiseIRCases pin the plain (non-compound) bitwise / shift binary
// operators — `& | ^ << >>` — on the IR path. They already lower through
// irlower's lower_expr (the IR binop set covers and/or/xor/shl/shr_s), and the
// compound forms `&= |= ^= <<= >>=` were pinned separately
// (self_host_compound_bitwise_ir_test.go); this is the complementary
// expression-position pin. The signal these isolate that the incidental uses in
// the uuid / url-codec / u32-wrap pins do not: the SIGNED-vs-UNSIGNED shift
// split. A right shift on a signed i32 is arithmetic (`shr_s` — sign-extends, so
// `-8 >> 1 == -4`), but on a u32 it is logical (`shr_u` — zero-fills, via the
// to_unsigned op remap), and a regression that picked the wrong one would slip
// through the algorithm-level pins. Each case is routing-pinned to "ir"
// (asm_pathprobe_run) and oracle-checked against the interpreter; every result
// stays <= 120 (cf. the wasmtime exit-code gap #2908).
var binaryBitwiseIRCases = []struct {
	name string
	main string
}{
	{"and", `function main(): i32 { var a = 12; var b = 10; return a & b; }`},
	{"or", `function main(): i32 { var a = 12; var b = 1; return a | b; }`},
	{"xor", `function main(): i32 { var a = 12; var b = 10; return a ^ b; }`},
	{"shl", `function main(): i32 { var a = 3; var b = 4; return a << b; }`},
	{"shr-pos", `function main(): i32 { var a = 100; var b = 2; return a >> b; }`},
	// signed i32 right shift is ARITHMETIC: -8 >> 1 == -4, so +100 == 96.
	{"shr-neg-arith", `function main(): i32 { var a = 0 - 8; var b = 1; return (a >> b) + 100; }`},
	// u32 right shift is LOGICAL (shr_u): 4000000000 >> 1 == 2000000000.
	{"u32-shr-logical", `function main(): i32 { var a: u32 = 4000000000u32; var b = a >> 1u32; if (b == 2000000000u32) { return 7; } return 1; }`},
	{"u32-and-mask", `function main(): i32 { var a: u32 = 4294967295u32; var b = a & 255u32; if (b == 255u32) { return 7; } return 1; }`},
	// operator precedence + nesting: ((5<<2)|1)&30 == (20|1)&30 == 21&30 == 20.
	{"combined-precedence", `function main(): i32 { var x = 5; var y = ((x << 2) | 1) & 30; return y; }`},
}

// TestSelfHostBinaryBitwiseIRX86_64 routes each case through the self-hosted
// x86-64 IR driver (asm_run), pins the routing to "ir" (asm_pathprobe_run), and
// oracle-checks the exit code against the interpreter.
func TestSelfHostBinaryBitwiseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range binaryBitwiseIRCases {
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

// TestNativeBinaryBitwiseX86_64 is the native-backend half: the same programs
// compiled through the Go compiler's x86-64 emitter must produce the same exit
// codes (pins the signed/unsigned shift split on the native side too).
func TestNativeBinaryBitwiseX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	for _, tc := range binaryBitwiseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main+"\n")
			_, code := compileAndRunX86_64(t, tc.main+"\n")
			if code != want {
				t.Errorf("%s native exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostBinaryBitwiseIRWasm runs the same cases through the wasm IR backend.
// wasm has DISTINCT instructions for the signed/unsigned shift split
// (`i32.shr_s` vs `i32.shr_u`), so a per-backend regression in the to_unsigned
// remap would surface here even if the register backends stayed correct. Mirrors
// the dual-backend shape of self_host_labeled_break_ir_test.go.
func TestSelfHostBinaryBitwiseIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binary-bitwise wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range binaryBitwiseIRCases {
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
			watFile := filepath.Join(dir, "binary_bitwise_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("binary-bitwise wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
