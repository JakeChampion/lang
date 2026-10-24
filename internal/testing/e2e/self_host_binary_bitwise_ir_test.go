package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binaryBitwiseIRCases = []struct {
	name string
	main string
}{
	{"and", `function main(): i32 { let a = 12; let b = 10; return a & b; }`},
	{"or", `function main(): i32 { let a = 12; let b = 1; return a | b; }`},
	{"xor", `function main(): i32 { let a = 12; let b = 10; return a ^ b; }`},
	{"shl", `function main(): i32 { let a = 3; let b = 4; return a << b; }`},
	{"shr-pos", `function main(): i32 { let a = 100; let b = 2; return a >> b; }`},
	{"shr-neg-arith", `function main(): i32 { let a = 0 - 8; let b = 1; return (a >> b) + 100; }`},
	{"u32-shr-logical", `function main(): i32 { let a: u32 = 4000000000u32; let b = a >> 1u32; if (b == 2000000000u32) { return 7; } return 1; }`},
	{"u32-and-mask", `function main(): i32 { let a: u32 = 4294967295u32; let b = a & 255u32; if (b == 255u32) { return 7; } return 1; }`},
	{"combined-precedence", `function main(): i32 { let x = 5; let y = ((x << 2) | 1) & 30; return y; }`},
}

func TestSelfHostBinaryBitwiseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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

func TestSelfHostBinaryBitwiseIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binary-bitwise wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range binaryBitwiseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
