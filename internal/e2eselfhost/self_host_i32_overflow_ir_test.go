package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i32OverflowIRCases exercise i32 signed-overflow WRAP through the self-host IR
// path (#3581). The self-host IR computed plain-i32 arithmetic in a 64-bit slot
// and never narrowed it, so `2147483647 + 1` kept the wide value (2147483648 >
// 0) while the native backend (and, after the checker fix, the AST interpreter
// oracle) wrapped to -2147483648. irlower now emits op_int_cast("i32") — the
// signed sibling of op_u32_wrap — after a plain-i32 +/-/*/<<, so every path
// agrees. Each case is oracle-checked against the interpreter, routing-pinned to
// "ir", and returns a small non-negative value.
var i32OverflowIRCases = []struct {
	name string
	main string
}{
	// Add overflow wraps to negative.
	{"add", `function main(): i32 { var x = 2147483647; var y = x + 1; if (y < 0) { return 1; } return 0; }`},
	// 65536 * 65536 == 2^32, which wraps to 0 in i32.
	{"mul", `function main(): i32 { var x = 65536; var y = x * x; if (y == 0) { return 5; } return 0; }`},
	// Left shift past bit 31 drops the high bits: 1 << 31 is INT_MIN (< 0).
	{"shl", `function main(): i32 { var x = 1; var y = x << 31; if (y < 0) { return 3; } return 0; }`},
	// Subtraction underflow wraps: -2e9 - 2e9 == -4e9, which wraps up to the
	// positive 294967296 in i32. (In-range literals throughout — a literal at
	// exactly INT_MIN's magnitude is a separate i32/i64-typing concern.)
	{"sub", `function main(): i32 { var x = 0 - 2000000000; var y = x - 2000000000; if (y > 0) { return 7; } return 0; }`},
}

// TestSelfHostI32OverflowIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostI32OverflowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range i32OverflowIRCases {
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

// TestSelfHostI32OverflowIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostI32OverflowIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i32-overflow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range i32OverflowIRCases {
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
			watFile := filepath.Join(dir, "i32ovf_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("i32 overflow wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
