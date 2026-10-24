package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const i64MeanReduceProgram = `function imean(arr: i32[]): i32 {
    let n: i32 = arr.len();
    if (n == 0) { return 0; }
    let s: i64 = 0;
    let i: i32 = 0;
    while (i < n) { s = s + (arr[i] as i64); i = i + 1; }
    return (s / (n as i64)) as i32;
}
function main(): i32 {
    if (imean([800000000, 800000000, 800000000]) == 800000000) { return 7; }
    return 0;
}`

func TestSelfHostI64MeanReduceIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")

	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")
	if got := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(i64MeanReduceProgram)))); got != "ir" {
		t.Fatalf("i64 mean reduction routed through %q path, want \"ir\"", got)
	}

	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	asm := runCapture(t, gcc, runner, driverBin, []byte(i64MeanReduceProgram))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "i64_mean", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("i64 mean reduction exited %d, want 7 (i64 accumulation must avoid i32 overflow)", code)
	}
}

func TestSelfHostI64MeanReduceIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-mean wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(i64MeanReduceProgram))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "i64_mean.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 7 {
		t.Errorf("i64 mean reduction wasm IR = %d, want 7", code)
	}
}
