package e2ecompiler

import (
	"bytes"
	"strings"
	"testing"
)

const processAliveSelfHostSource = `function main(): i32 {
    // The process's OWN pid, read from the kernel rather than guessed: field 1
    // of /proc/self/stat is the pid of the process doing the reading.
    let st: string = "";
    match (read_file("/proc/self/stat")) {
        Ok(v) => { st = v; },
        Err(e) => { return 10; }
    }
    let fields: string[] = st.split(" ");
    let me: i32 = 0;
    for b in fields[0] { me = me * 10 + (b as i32) - 48; }
    if (me <= 0) { return 11; }
    if (!process_alive(me)) { return 1; }

    // 2^22 is one above the largest pid_max Linux accepts, so no process can
    // ever hold it — where a pid merely unused right now could be reassigned.
    if (process_alive(4194304)) { return 2; }

    // A pid that genuinely died: fork a child, ask while it is still in the
    // process table (running or a zombie — kill(2) sees both), then reap it and
    // ask again.
    let kid: i32 = proc_fork();
    if (kid == 0) { exit(0); }
    if (kid < 0) { return 12; }
    if (!process_alive(kid)) { return 3; }
    let status: i32 = proc_waitpid(kid);
    if (process_alive(kid)) { return 4; }

    // kill(2) reads these as process GROUPS, not processes, so they answer
    // false without a syscall.
    if (process_alive(0)) { return 5; }
    if (process_alive(0 - 1)) { return 6; }
    return 0;
}`

const processAliveWasmProbe = `function main(): i32 {
    if (process_alive(1)) { return 0; }
    return 1;
}`

func TestSelfHostProcessAliveIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, processAliveSelfHostSource)
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

func TestSelfHostProcessAliveIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, processAliveSelfHostSource, "-target", "arm64-linux")
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

func TestSelfHostProcessAliveIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin)
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
	if !strings.Contains(stderr.String(), "required by `process_alive`; targets providing it") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
