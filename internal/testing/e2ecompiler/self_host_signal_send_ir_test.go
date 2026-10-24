package e2ecompiler

import (
	"bytes"
	"strings"
	"testing"
)

const signalSendSelfHostSource = `function main(): i32 {
    // pid 0 is the caller's OWN process group, which it may always signal, and
    // signal 0 delivers nothing. process_alive answers false for this same
    // argument; signal_send must not, because a group is what a sender means
    // by it.
    match (signal_send(0, 0)) {
        Ok(_) => {},
        Err(e) => { return 1; }
    }

    // 2^22 is one above the largest pid_max Linux accepts, so no process can
    // ever hold it. ESRCH has no named IoError variant, so it arrives as
    // Other(path, strerror, errno) with an empty path.
    match (signal_send(4194304, 0)) {
        Ok(_) => { return 2; },
        Err(e) => {
            match (e) {
                Other(p, msg, _) => {
                    if (p.len() != 0) { return 3; }
                    if (msg != "No such process") { return 4; }
                },
                _ => { return 5; }
            }
        }
    }

    // An impossible signal number against a target that DOES exist: EINVAL.
    // The target has to exist for this to hold on Linux, which looks the pid
    // up first and answers ESRCH for a bad signal to a missing process.
    match (signal_send(0, 12345)) {
        Ok(_) => { return 6; },
        Err(e) => {
            match (e) {
                Other(p, msg, _) => {
                    if (msg != "Invalid argument") { return 7; }
                },
                _ => { return 8; }
            }
        }
    }

    // Delivery. The child sleeps far past anything the parent needs, so the
    // only way it reaches the parent's waitpid is the signal.
    let kid: i32 = proc_fork();
    if (kid == 0) {
        sleep_ms(60000 as i64);
        exit(70);
    }
    if (kid < 0) { return 9; }
    match (signal_send(kid, 9)) {
        Ok(_) => {},
        Err(e) => { return 10; }
    }
    // proc_waitpid reports a signal death as 128+signal.
    let status: i32 = proc_waitpid(kid);
    if (status != 137) { return 11; }
    return 0;
}`

const signalSendWasmProbe = `function main(): i32 {
    match (signal_send(1, 0)) {
        Ok(_) => { return 0; },
        Err(e) => { return 1; }
    }
}`

func TestSelfHostSignalSendIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, signalSendSelfHostSource)
	if !bytes.Contains(asm, []byte("call __fn___fern_signal_send")) {
		t.Fatalf("emitted asm has no `call __fn___fern_signal_send` — signal_send did not lower through the x86-64 IR path")
	}
	progBin := buildBin(t, gcc, dir, "sigsend_prog", string(asm))
	run := runX86_64Bin(runner, progBin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see signalSendSelfHostSource)\n%s", code, out)
	}
}

func TestSelfHostSignalSendIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, signalSendSelfHostSource, "-target", "arm64-linux")
	if !bytes.Contains(asm, []byte("bl __fn___fern_signal_send")) {
		t.Fatalf("emitted asm has no `bl __fn___fern_signal_send` — signal_send did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "sigsend_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see signalSendSelfHostSource)\n%s", code, out)
	}
}

func TestSelfHostSignalSendIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin)
	cmd.Stdin = strings.NewReader(signalSendWasmProbe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("wasm driver accepted signal_send; a component has no process table")
	}
	if !strings.Contains(stderr.String(), "required by `signal_send`; targets providing it") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
