package e2ecompiler

import (
	"bytes"
	"strings"
	"testing"
)

const procWaitpidNohangSelfHostSource = `function main(): i32 {
    // No children at all: -ECHILD, not -1 — which would claim a child is
    // still running.
    if (proc_waitpid_nohang(0 - 1) != 0 - 10) { return 1; }

    let kid: i32 = proc_fork();
    if (kid < 0) { return 2; }
    if (kid == 0) {
        sleep_ms(400 as i64);
        exit(7);
    }
    // Alive, nothing to report: returns rather than blocking, and returns
    // -1 where wait4 itself answers 0.
    if (proc_waitpid_nohang(kid) != 0 - 1) { return 3; }

    // The blocking wait on -1 reaps whoever exits first and reports the
    // status without the pid; the probe below is how the pid comes back.
    if (proc_waitpid(0 - 1) != 7) { return 4; }
    if (proc_waitpid_nohang(kid) != 0 - 10) { return 5; }

    let k2: i32 = proc_fork();
    if (k2 < 0) { return 6; }
    if (k2 == 0) {
        sleep_ms(60000 as i64);
        exit(70);
    }
    match (signal_send(k2, 9)) {
        Ok(_) => {},
        Err(e) => { return 7; }
    }
    let tries: i32 = 0;
    let got: i32 = 0 - 1;
    while (tries < 5000) {
        got = proc_waitpid_nohang(k2);
        if (got != 0 - 1) { break; }
        sleep_ms(1 as i64);
        tries = tries + 1;
    }
    if (got != 137) { return 8; }
    return 0;
}`

const procWaitpidNohangWasmProbe = `function main(): i32 {
    return proc_waitpid_nohang(1);
}`

func TestSelfHostProcWaitpidNohangIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, procWaitpidNohangSelfHostSource)
	if !bytes.Contains(asm, []byte("call __fn___fern_proc_waitpid_nohang")) {
		t.Fatalf("emitted asm has no `call __fn___fern_proc_waitpid_nohang` — proc_waitpid_nohang did not lower through the x86-64 IR path")
	}
	progBin := buildBin(t, gcc, dir, "nohang_prog", string(asm))
	run := runX86_64Bin(runner, progBin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see procWaitpidNohangSelfHostSource)\n%s", code, out)
	}
}

func TestSelfHostProcWaitpidNohangIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, procWaitpidNohangSelfHostSource, "-target", "arm64-linux")
	if !bytes.Contains(asm, []byte("bl __fn___fern_proc_waitpid_nohang")) {
		t.Fatalf("emitted asm has no `bl __fn___fern_proc_waitpid_nohang` — proc_waitpid_nohang did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "nohang_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see procWaitpidNohangSelfHostSource)\n%s", code, out)
	}
}

func TestSelfHostProcWaitpidNohangIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin)
	cmd.Stdin = strings.NewReader(procWaitpidNohangWasmProbe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("wasm driver accepted proc_waitpid_nohang; a component has no process model")
	}
	if !strings.Contains(stderr.String(), "required by `proc_waitpid_nohang`; targets providing it") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
