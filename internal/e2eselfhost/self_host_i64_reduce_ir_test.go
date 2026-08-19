package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i64MeanReduceProgram is the IR-path-eligible computational core of the
// std/array `avg()` overflow fix (#2687): a counted reduction that accumulates
// i32 array elements into an i64, divides by an i64 count, and narrows the
// truncated mean back to i32. The three elements sum to 2.4e9 — which overflows
// i32 (> 2^31-1) and would wrap to a NEGATIVE mean under i32 accumulation — so
// the program returns 7 ONLY if the i64 accumulation / division / narrowing all
// lower correctly. This pins that the self-hosted IR path (irlower.lower_i64:
// `as i64` widening of an i32 array element, i64 `+`, i64 `/`, `as i32`
// narrowing) handles the shape the stdlib fix relies on, on every backend.
//
// avg() itself returns Option[i32], which is not IR-eligible yet (a separate
// slice), so the stdlib method rides the AST path on the self-host today; this
// reduction returns a bare i32, so it stays on the IR path where the i64
// arithmetic is exercised directly.
const i64MeanReduceProgram = `function imean(arr: i32[]): i32 {
    var n: i32 = arr.len();
    if (n == 0) { return 0; }
    var s: i64 = 0;
    var i: i32 = 0;
    while (i < n) { s = s + (arr[i] as i64); i = i + 1; }
    return (s / (n as i64)) as i32;
}
function main(): i32 {
    if (imean([800000000, 800000000, 800000000]) == 800000000) { return 7; }
    return 0;
}`

// TestSelfHostI64MeanReduceIRPathX86_64 first probes (via asm_pathprobe_run)
// that the program is fully IR-eligible — so a future change that silently
// kicked it off the IR path would fail here —
// then runs it through the self-hosted x86-64 driver and asserts exit 7.
func TestSelfHostI64MeanReduceIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")

	// Probe: the module must route through the IR path.
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")
	if got := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(i64MeanReduceProgram)))); got != "ir" {
		t.Fatalf("i64 mean reduction routed through %q path, want \"ir\"", got)
	}

	// Run: the self-host x86-64 driver must emit a binary that returns 7.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
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

// TestSelfHostI64MeanReduceIRWasm runs the same reduction through the wasm IR
// backend (wasm_ir_run -ir forces the IR path): the i64 accumulation / division
// / narrowing must produce the same answer on the stack-machine backend.
func TestSelfHostI64MeanReduceIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-mean wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
