package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
	"time"
)

// TestSelfHostSleepMsWideCount covers #9492: the register runtime body behind
// sleep_ms declared `ms: i32` while the builtin's parameter — and the IR call —
// is i64, so any count past the i32 range was truncated to its low word before
// the body looked at it.
//
// The count here is 2^32 ms, 49.7 days, chosen because its low word is exactly
// zero: a truncated count takes the `ms <= 0` early-out and the program runs to
// completion in microseconds, where an honoured one is still sleeping. So the
// assertion is that the process is STILL RUNNING after a short deadline, which
// is the observable difference between the two and costs the suite that
// deadline rather than the sleep.
//
// TestSelfHostSleepMsI64IR pins the i64 argument reaching the runtime, but
// sleeps 1 ms — a count the truncation cannot distort, which is why it stayed
// green throughout.
func TestSelfHostSleepMsWideCount(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    sleep_ms((1 as i64) << (32 as i64));
    return 7;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, diagnostics.String())
	}
	progBin := buildBin(t, gcc, dir, "sleepms_wide", string(asm))

	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	if err := run.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = run.Wait(); close(done) }()
	select {
	case <-done:
		t.Errorf("sleep_ms(2^32) returned after %v (exit %d): the count was truncated to its "+
			"low word and took the non-positive early-out (#9492)",
			sleepMsWideDeadline, run.ProcessState.ExitCode())
	case <-time.After(sleepMsWideDeadline):
		// Still sleeping, which is the whole of what this asserts. Nothing is
		// going to wake it in this suite's lifetime, so end it here.
		_ = run.Process.Kill()
		<-done
	}
}

// sleepMsWideDeadline is how long the program above must stay asleep. It only
// has to outlast the microseconds a truncated count returns in; the honoured
// count is seven weeks, so there is no value in waiting longer.
const sleepMsWideDeadline = 500 * time.Millisecond
