package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostSignalDispositionIRWasm pins signal_ignore / signal_default on
// the self-host's wasm IR path (#8792), and the two reads — signal_mask and
// signal_disposition — that this PR adds and that would otherwise lower
// untested.
//
// Nothing in either WASI world can deliver a signal, so all four lower to a
// no-op that drops their arguments and leaves a zero behind — the honest
// answer rather than a stub for a missing import, the same shape as
// hostname()'s "". What that leaves to assert is the STACK EFFECT: the
// arithmetic around the calls is what a wrong one would corrupt, and it is
// checked by the exit code rather than by reading the WAT, so a lowering that
// emitted the right text with the wrong balance still fails.
func TestSelfHostSignalDispositionIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host signal-disposition wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    var n: i32 = 7;
    signal_ignore(13);
    n = n + 1;
    signal_default(13);
    n = n + 1;
    signal_ignore(2);
    // A component's blocked set is empty and every disposition sits at its
    // default, so both reads answer 0 — and each wrong stack balance would
    // corrupt the arithmetic around it.
    var b1: i64 = signal_mask(1, 2 as i64);
    n = n + 1;
    var d1: i32 = signal_disposition(2);
    if ((b1 != 0 as i64) || (d1 != 0)) { return 1; }
    n = n + 1;
    if (n != 11) { return 1; }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
