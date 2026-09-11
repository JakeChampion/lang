package e2eselfhost

import (
	"bytes"
	"strings"
	"testing"
)

// `process_alive` (#8767) through the SELF-HOST IR path, on each backend that
// emits it. The op pops a pid and pushes 0/1 from one `kill(pid, 0)` —
// asmcore.rt_src_process_alive, reached as `call __fn___fern_process_alive`.
//
// The probe MEASURES against pids whose liveness the running program itself
// established, rather than asserting the builtin compiles. Every wrong
// lowering still compiles and links:
//
//   - lowering to nothing leaves the operand on the stack and answers with
//     whatever was under it,
//   - the wrong syscall number answers -ENOSYS for every pid, which is neither
//     0 nor -EPERM, so everything reads dead,
//   - dropping the -EPERM arm reads a process owned by another user as dead,
//   - passing a non-positive pid through asks kill(2) about a process GROUP
//     and reports the caller's own group as alive.
//
// The four probes between them separate all of those: the process's own pid is
// alive, a pid the program forked and reaped is not, a pid above the kernel's
// ceiling never was, and 0 / -1 are false without a syscall.
//
// EPERM is NOT reachable from here: this container runs as uid 0, so kill(1, 0)
// succeeds outright rather than being refused, and a test process cannot drop
// to another uid and still be the thing under test. Native's
// internal/e2e/process_alive_test.go has the same gap for the same reason.
//
// wasm's whole answer is a refusal: a component has no process table, so
// platforms.fern withholds the builtin on `proc`.
// TestSelfHostProcessAliveIRWasmRefused is that half.
const processAliveSelfHostSource = `function main(): i32 {
    // The process's OWN pid, read from the kernel rather than guessed: field 1
    // of /proc/self/stat is the pid of the process doing the reading.
    var st: string = "";
    match (read_file("/proc/self/stat")) {
        Ok(v) => { st = v; },
        Err(e) => { return 10; }
    }
    var fields: string[] = st.split(" ");
    var me: i32 = str_to_i32(fields[0]);
    if (me <= 0) { return 11; }
    if (!process_alive(me)) { return 1; }

    // 2^22 is one above the largest pid_max Linux accepts, so no process can
    // ever hold it — where a pid merely unused right now could be reassigned.
    if (process_alive(4194304)) { return 2; }

    // A pid that genuinely died: fork a child, ask while it is still in the
    // process table (running or a zombie — kill(2) sees both), then reap it and
    // ask again.
    var kid: i32 = proc_fork();
    if (kid == 0) { exit(0); }
    if (kid < 0) { return 12; }
    if (!process_alive(kid)) { return 3; }
    var status: i32 = proc_waitpid(kid);
    if (process_alive(kid)) { return 4; }

    // kill(2) reads these as process GROUPS, not processes, so they answer
    // false without a syscall.
    if (process_alive(0)) { return 5; }
    if (process_alive(0 - 1)) { return 6; }
    return 0;
}`

// processAliveWasmProbe is the refusal probe: process_alive and nothing else,
// because wasm_unsupported_builtin reports the FIRST unsupported name it finds
// and the measuring probe above forks.
const processAliveWasmProbe = `function main(): i32 {
    if (process_alive(1)) { return 0; }
    return 1;
}`

// TestSelfHostProcessAliveIRX86_64 compiles the probe through the production
// x86-64 IR driver and runs it.
func TestSelfHostProcessAliveIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, processAliveSelfHostSource, "-ir")
	if !bytes.Contains(asm, []byte("call __fn___fern_process_alive")) {
		t.Fatalf("emitted asm has no `call __fn___fern_process_alive` — process_alive did not lower through the x86-64 IR path")
	}
	progBin := buildBin(t, gcc, dir, "procalive_prog", string(asm))
	run := runX86_64Bin(runner, progBin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see processAliveSelfHostSource)\n%s", code, out)
	}
}

// TestSelfHostProcessAliveIRArm64 runs the same probe through the arm64 IR
// backend under qemu, where the helper's syscall number is asm-generic 129.
func TestSelfHostProcessAliveIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, processAliveSelfHostSource, "-target", "arm64-linux", "-ir")
	if !bytes.Contains(asm, []byte("bl __fn___fern_process_alive")) {
		t.Fatalf("emitted asm has no `bl __fn___fern_process_alive` — process_alive did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "procalive_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see processAliveSelfHostSource)\n%s", code, out)
	}
}

// TestSelfHostProcessAliveIRWasmRefused pins the wasm answer, which is a
// refusal NAMING THE BUILTIN. A component has no process table, so
// platforms.fern withholds `proc`; the wasm driver has to say so itself,
// because instruction selection downstream sees only an IR op and would frame
// a deliberate gap as a missing lowering.
func TestSelfHostProcessAliveIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin, "-ir")
	cmd.Stdin = strings.NewReader(processAliveWasmProbe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("wasm driver accepted process_alive; a component has no process table")
	}
	if !strings.Contains(stderr.String(), "process_alive is not supported on the wasm target") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
