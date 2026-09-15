package e2eselfhost

import (
	"bytes"
	"strings"
	"testing"
)

// `proc_waitpid_nohang` (#8374) through the SELF-HOST IR path, on each backend
// that emits it. The op pops [pid] and pushes an i32 from one wait4 with
// WNOHANG — asmcore.rt_src_proc_waitpid_nohang, reached as
// `call __fn___fern_proc_waitpid_nohang`.
//
// The probe measures all three answers rather than asserting the builtin
// compiles, because a lowering that dropped WNOHANG would still compile and
// would still satisfy anything that only waited for a child which does exit:
//
//   - a live child answers -1 rather than blocking, which is the whole point
//     (and a dropped WNOHANG HANGS here rather than failing, which is the
//     loudest failure available),
//   - -1 rather than 0, which is what wait4 itself returns there and what a
//     clean exit decodes to,
//   - a reaped child answers -ECHILD, which is what recovers the pid a
//     blocking `proc_waitpid(-1)` drops,
//   - a signal death decodes to 128+signal, as the blocking sibling does.
//
// wasm's whole answer is a refusal: a component has no process model to reap
// in, so platforms.fern withholds the builtin on `proc`.
// TestSelfHostProcWaitpidNohangIRWasmRefused is that half.
const procWaitpidNohangSelfHostSource = `function main(): i32 {
    // No children at all: -ECHILD, not -1 — which would claim a child is
    // still running.
    if (proc_waitpid_nohang(0 - 1) != 0 - 10) { return 1; }

    var kid: i32 = proc_fork();
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

    var k2: i32 = proc_fork();
    if (k2 < 0) { return 6; }
    if (k2 == 0) {
        sleep_ms(60000 as i64);
        exit(70);
    }
    match (signal_send(k2, 9)) {
        Ok(_) => {},
        Err(e) => { return 7; }
    }
    var tries: i32 = 0;
    var got: i32 = 0 - 1;
    while (tries < 5000) {
        got = proc_waitpid_nohang(k2);
        if (got != 0 - 1) { break; }
        sleep_ms(1 as i64);
        tries = tries + 1;
    }
    if (got != 137) { return 8; }
    return 0;
}`

// procWaitpidNohangWasmProbe is the refusal probe: the one builtin and nothing
// else, because wasm_unsupported_builtin reports the FIRST unsupported name it
// finds and the measuring probe above forks.
const procWaitpidNohangWasmProbe = `function main(): i32 {
    return proc_waitpid_nohang(1);
}`

// TestSelfHostProcWaitpidNohangIRX86_64 compiles the probe through the
// production x86-64 IR driver and runs it.
func TestSelfHostProcWaitpidNohangIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, procWaitpidNohangSelfHostSource, "-ir")
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

// TestSelfHostProcWaitpidNohangIRArm64 runs the same probe through the arm64
// IR backend under qemu, where wait4 is asm-generic 260.
func TestSelfHostProcWaitpidNohangIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, procWaitpidNohangSelfHostSource, "-target", "arm64-linux", "-ir")
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

// TestSelfHostProcWaitpidNohangIRWasmRefused pins the wasm answer, which is a
// refusal NAMING THE BUILTIN. A component has no process model, so
// platforms.fern withholds `proc`; the wasm driver has to say so itself,
// because instruction selection downstream sees only an IR op and would frame
// a deliberate gap as a missing lowering.
func TestSelfHostProcWaitpidNohangIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin, "-ir")
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
	if !strings.Contains(stderr.String(), "proc_waitpid_nohang is not supported on the wasm target") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
