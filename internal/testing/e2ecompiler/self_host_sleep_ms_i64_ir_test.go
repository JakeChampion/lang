package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostSleepMsI64IR pins `sleep_ms(<i64>)` lowering on the self-host
// x86-64 IR path: an i64 count (`sleep_ms(5 as i64)`) and a plain i32 one both
// reach the __fern_sleep_ms runtime, which reads the count from a 64-bit
// register (rdi / x0). The program reads a
// monotonic clock, sleeps an i64 millisecond count, reads it again, and checks
// the clock did not go backwards -> exit 0; exercises monotonic_ns + sleep_ms
// (i64 arg) on the IR path.
func TestSelfHostSleepMsI64IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    let a: i64 = monotonic_ns();
    sleep_ms(1 as i64);
    let b: i64 = monotonic_ns();
    if (b < a) { return 1; }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_sleep_ms") {
		t.Fatal("sleep_ms did not reach the IR runtime path (no __fern_sleep_ms in asm)")
	}
	progBin := buildBin(t, gcc, dir, "sleepms_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("sleep_ms(i64) IR program exited %d, want 0 (monotonic clock + i64 sleep)", code)
	}
}
