package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrLitIndexCases = []struct {
	name string
	src  string
	want int
}{
	{"f64-literal-index", "function main(): i32 {\n" +
		"    let i: i32 = 1;\n" +
		"    let x: f64 = [10.5, 20.5, 30.5][i];\n" +
		"    if (x > 20.0 && x < 21.0) { return 42; }\n" +
		"    return 1;\n" +
		"}\n", 42},
	{"i64-literal-index", "function main(): i32 {\n" +
		"    let i: i32 = 2;\n" +
		"    let x: i64 = [100 as i64, 200 as i64, 300 as i64][i];\n" +
		"    if (x == (300 as i64)) { return 43; }\n" +
		"    return 1;\n" +
		"}\n", 43},
}

func TestSelfHostArrayLiteralIndexIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("single-program IR driver test runs only natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrLitIndexCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(driverBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			run := exec.Command(progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s (x86-64 IR) exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrayLiteralIndexIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-literal-index wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrLitIndexCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			run.Dir = dir
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s (wasm IR) exited %d, want %d — mis-stride regression\n--- WAT ---\n%s", tc.name, code, tc.want, wat)
			}
		})
	}
}
