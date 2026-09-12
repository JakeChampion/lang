package e2eselfhost

import (
	"bytes"
	"strings"
	"testing"
)

// `signal_send` (#8377) through the SELF-HOST IR path, on each backend that
// emits it. The op pops [pid, sig] and pushes Result[void, IoError] from one
// kill(2) — asmcore.rt_src_signal_send, reached as
// `call __fn___fern_signal_send`.
//
// The probe MEASURES delivery and the errno classification rather than
// asserting the builtin compiles, because every wrong lowering still compiles
// and links:
//
//   - operands pushed in the wrong order signals the SIGNAL number's process,
//     which the delivery arm catches: the child outlives the call,
//   - the wrong syscall number is -ENOSYS for everything, so nothing is
//     delivered and no errno is the one expected,
//   - filtering the pid the way process_alive does turns pid 0 from "my own
//     process group" into a refusal, which the first arm catches,
//   - dropping the Err arm loses the errno the caller reads its diagnostic
//     out of.
//
// wasm's whole answer is a refusal: a component has no process table to
// deliver to, so platforms.fern withholds the builtin on `proc`.
// TestSelfHostSignalSendIRWasmRefused is that half.
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
    // Other(path, strerror) with an empty path.
    match (signal_send(4194304, 0)) {
        Ok(_) => { return 2; },
        Err(e) => {
            match (e) {
                Other(p, msg) => {
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
                Other(p, msg) => {
                    if (msg != "Invalid argument") { return 7; }
                },
                _ => { return 8; }
            }
        }
    }

    // Delivery. The child sleeps far past anything the parent needs, so the
    // only way it reaches the parent's waitpid is the signal.
    var kid: i32 = proc_fork();
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
    var status: i32 = proc_waitpid(kid);
    if (status != 137) { return 11; }
    return 0;
}`

// signalSendWasmProbe is the refusal probe: signal_send and nothing else,
// because wasm_unsupported_builtin reports the FIRST unsupported name it finds
// and the measuring probe above forks.
const signalSendWasmProbe = `function main(): i32 {
    match (signal_send(1, 0)) {
        Ok(_) => { return 0; },
        Err(e) => { return 1; }
    }
}`

// TestSelfHostSignalSendIRX86_64 compiles the probe through the production
// x86-64 IR driver and runs it.
func TestSelfHostSignalSendIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, signalSendSelfHostSource, "-ir")
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

// TestSelfHostSignalSendIRArm64 runs the same probe through the arm64 IR
// backend under qemu, where the helper's syscall number is asm-generic 129.
func TestSelfHostSignalSendIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, signalSendSelfHostSource, "-target", "arm64-linux", "-ir")
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

// TestSelfHostSignalSendIRWasmRefused pins the wasm answer, which is a refusal
// NAMING THE BUILTIN. A component has no process table, so platforms.fern
// withholds `proc`; the wasm driver has to say so itself, because instruction
// selection downstream sees only an IR op and would frame a deliberate gap as
// a missing lowering.
func TestSelfHostSignalSendIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin, "-ir")
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
	if !strings.Contains(stderr.String(), "signal_send is not supported on the wasm target") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
