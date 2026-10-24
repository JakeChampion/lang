package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const sleepNsSelfHostSource = `function main(): i32 {
    let t0: i64 = monotonic_ns();
    sleep_ns(200000 as i64);
    let d0: i64 = monotonic_ns() - t0;
    if (d0 < (200000 as i64)) { return 1; }

    let t1: i64 = monotonic_ns();
    sleep_ns(30000000 as i64);
    let d1: i64 = monotonic_ns() - t1;
    if (d1 < (30000000 as i64)) { return 2; }

    let zero: i64 = 0 as i64;
    let neg: i64 = zero - (5 as i64);
    let t2: i64 = monotonic_ns();
    sleep_ns(zero);
    sleep_ns(neg);
    sleep_ns(0);
    let d2: i64 = monotonic_ns() - t2;
    if (d2 > (500000000 as i64)) { return 3; }
    return 0;
}`

func runSleepNsDriver(t *testing.T, runner []string, driverBin, src string, args ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, args...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("self-host driver failed: %v\n%s", err, out)
	}
	return out
}

func TestSelfHostSleepNsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSleepNsDriver(t, runner, driverBin, sleepNsSelfHostSource)
	if !bytes.Contains(asm, []byte("call __fn___fern_sleep_ns")) {
		t.Fatalf("emitted asm has no `call __fn___fern_sleep_ns` — sleep_ns did not lower through the x86-64 IR path")
	}
	progBin := buildBin(t, gcc, dir, "sleepns_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see sleepNsSelfHostSource)", code)
	}
}

func TestSelfHostSleepNsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSleepNsDriver(t, x86runner, driverBin, sleepNsSelfHostSource, "-target", "arm64-linux")
	if !bytes.Contains(asm, []byte("bl __fn___fern_sleep_ns")) {
		t.Fatalf("emitted asm has no `bl __fn___fern_sleep_ns` — sleep_ns did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "sleepns_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see sleepNsSelfHostSource)", code)
	}
}

func TestSelfHostSleepNsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sleep_ns wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	wat := runSleepNsDriver(t, runner, driverBin, sleepNsSelfHostSource)
	if !bytes.Contains(wat, []byte("call $__fern_sleep_ns")) {
		t.Fatal("sleep_ns did not reach the wasm IR runtime path (no call $__fern_sleep_ns in WAT)")
	}
	watFile := filepath.Join(dir, "sleepns_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see sleepNsSelfHostSource)\n--- WAT ---\n%s", code, wat)
	}
}
