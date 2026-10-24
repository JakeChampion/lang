package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
	"time"
)

func TestSelfHostSleepMsWideCount(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    sleep_ms((1 as i64) << (32 as i64));
    return 7;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
		_ = run.Process.Kill()
		<-done
	}
}

const sleepMsWideDeadline = 500 * time.Millisecond
