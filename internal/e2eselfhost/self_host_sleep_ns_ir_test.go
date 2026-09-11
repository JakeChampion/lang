package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `sleep_ns` (#8528) through the SELF-HOST IR path, on each backend that emits
// it. The op mirrors `sleep_ms` everywhere except the split: the timespec
// divisor is 1e9 and the remainder IS tv_nsec, so a sub-millisecond interval
// reaches the kernel unrounded.
//
// Every leg MEASURES rather than checking an exit code alone. A `sleep_ns` that
// lowered to nothing, or reused sleep_ms's 1e3 divisor, or stored the remainder
// in the wrong timespec word, compiles and links and still returns the sentinel
// — it just pauses for the wrong length, or not at all. Only a clock on both
// sides of the call can tell those apart.
//
// Every bound is ONE-SIDED. nanosleep may overshoot by any amount the scheduler
// likes (and this runs under qemu and wasmtime, which like it a lot), so "not
// shorter than asked" is the only property the helper owns.
//
// The bare `sleep_ns(0)` is not a third no-op for its own sake: the count is an
// i64 in the signature, so an i32 leaf has to WIDEN on the way down rather than
// bail the module off the IR path, and that is the shape that would.
//
// The 1e3-divisor mutant is caught as a HANG rather than a diff — asking for
// 200 seconds instead of 200 µs — which the package timeout reports. That is
// the same trade internal/e2e/sleep_ns_test.go makes on the native side.
const sleepNsSelfHostSource = `function main(): i32 {
    var t0: i64 = monotonic_ns();
    sleep_ns(200000 as i64);
    var d0: i64 = monotonic_ns() - t0;
    if (d0 < (200000 as i64)) { return 1; }

    var t1: i64 = monotonic_ns();
    sleep_ns(30000000 as i64);
    var d1: i64 = monotonic_ns() - t1;
    if (d1 < (30000000 as i64)) { return 2; }

    var zero: i64 = 0 as i64;
    var neg: i64 = zero - (5 as i64);
    var t2: i64 = monotonic_ns();
    sleep_ns(zero);
    sleep_ns(neg);
    sleep_ns(0);
    var d2: i64 = monotonic_ns() - t2;
    if (d2 > (500000000 as i64)) { return 3; }
    return 0;
}`

// runDriver feeds src to a self-host driver binary and returns what it emitted.
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

// TestSelfHostSleepNsIRX86_64 compiles the probe through the production x86-64
// IR driver and runs it. The op emits `call __fn___fern_sleep_ns`, whose helper
// is asmcore.rt_src_sleep_ns — nanosleep over a timespec written with
// __store_i64, so a count past the i32 range is not truncated on the way in.
func TestSelfHostSleepNsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSleepNsDriver(t, runner, driverBin, sleepNsSelfHostSource, "-ir")
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

// TestSelfHostSleepNsIRArm64 runs the same probe through the arm64 IR backend
// under qemu. The arm64 op handler emits `bl __fn___fern_sleep_ns` and the
// runtime block emits the helper on the sleep_ns need.
func TestSelfHostSleepNsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSleepNsDriver(t, x86runner, driverBin, sleepNsSelfHostSource, "-target", "arm64-linux", "-ir")
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

// TestSelfHostSleepNsIRWasm is the leg where the resolution is exact: preview1's
// subscription timeout is ALREADY nanoseconds, so $__fern_sleep_ns is
// $__fern_sleep_ms without the i64.mul. That makes the scale the whole
// difference between the two bodies, and the 200 µs step is what notices if the
// multiply was copied across with the rest.
func TestSelfHostSleepNsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sleep_ns wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	wat := runSleepNsDriver(t, runner, driverBin, sleepNsSelfHostSource, "-ir")
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
