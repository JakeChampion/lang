package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var f32CastCases = []struct {
	name     string
	src      string
	expected int
}{
	{"lit-round", `function main(): i32 { let x: f32 = 16777217.0 as f32; if ((x as f64) == 16777216.0) { return 1; } return 0; }`, 1},
	{"int-round", `function main(): i32 { let x: f32 = 16777217 as f32; if ((x as f64) == 16777216.0) { return 1; } return 0; }`, 1},
	{"nonbind-round", `function chk(v: f64): i32 { if (v == 16777216.0) { return 1; } return 0; } function main(): i32 { return chk((16777217.0 as f32) as f64); }`, 1},
	{"exact-small", `function main(): i32 { let a: f32 = 2.5 as f32; return a as i32; }`, 2},
	{"exact-int", `function main(): i32 { let a: f32 = 5 as f32; return a as i32; }`, 5},
	{"f64-identity", `function main(): i32 { let x: f64 = 3.5 as f64; return x as i32; }`, 3},
}

func TestSelfHostF32CastWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f32-cast wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		watFile := filepath.Join(dir, "f32_prog.wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %q:\n%s", src, wat)
		}
		return run.ProcessState.ExitCode()
	}

	for _, tc := range f32CastCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f32-cast wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

func TestSelfHostF32CastX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "f32_inner.s")
		innerBin := filepath.Join(dir, "f32_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc for %q: %v\n%s", src, err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	for _, tc := range f32CastCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f32-cast x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
