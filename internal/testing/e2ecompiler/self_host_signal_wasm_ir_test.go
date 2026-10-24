package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostSignalDispositionIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host signal-disposition wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    let n: i32 = 7;
    signal_ignore(13);
    n = n + 1;
    signal_default(13);
    n = n + 1;
    signal_ignore(2);
    // A component's blocked set is empty and every disposition sits at its
    // default, so both reads answer 0 — and each wrong stack balance would
    // corrupt the arithmetic around it.
    let b1: i64 = signal_mask(1, 2 as i64);
    n = n + 1;
    let d1: i32 = signal_disposition(2);
    if ((b1 != 0 as i64) || (d1 != 0)) { return 1; }
    n = n + 1;
    if (n != 11) { return 1; }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "signal_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("signal-disposition wasm IR program exited %d, want 0\n--- WAT ---\n%s", code, wat)
	}
}
