package e2eselfhost

import (
	"bytes"
	"strings"
	"testing"
)

// `set_process_group` (#8374) through the SELF-HOST IR path, on each backend
// that emits it. The op pops [pid, pgid] and pushes Result[void, IoError] from
// one setpgid(2) — asmcore.rt_src_set_process_group, reached as
// `call __fn___fern_set_process_group`.
//
// The probe MEASURES the group it makes and the errno classification rather
// than asserting the builtin compiles, because every wrong lowering still
// compiles and links:
//
//   - operands pushed in the wrong order swaps the two argument errors:
//     setpgid(4194304, 0) is ESRCH where setpgid(0, 4194304) is EPERM, so the
//     probe holds both and a transposition fails both arms,
//   - the wrong syscall number is -ENOSYS for everything, so no arm gets the
//     errno it expects and the first arm's Ok never arrives,
//   - a group that was never actually made leaves the child unsignallable,
//     which the last arm catches: it kills the GROUP, not the pid,
//   - dropping the Err arm loses the errno the caller reads its diagnostic
//     out of.
//
// wasm's whole answer is a refusal: a component has no process table to hold a
// group, so platforms.fern withholds the builtin on `proc`.
// TestSelfHostSetProcessGroupIRWasmRefused is that half.
const setProcessGroupSelfHostSource = `function main(): i32 {
    // pid 0 is the caller and pgid 0 the pid's own value, so this is a
    // process moving into a fresh group of its own — which anything but a
    // session leader may always do.
    match (set_process_group(0, 0)) {
        Ok(_) => {},
        Err(e) => { return 1; }
    }

    // 2^22 is one above the largest pid_max Linux accepts, so no process can
    // ever hold it. ESRCH has no named IoError variant, so it arrives as
    // Other(path, strerror) with an empty path: the op took two integers and
    // never saw a file.
    match (set_process_group(4194304, 0)) {
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

    // The same number on the other side of the call names no group in this
    // session: EPERM, not ESRCH. This is the arm a transposed emit fails.
    match (set_process_group(0, 4194304)) {
        Ok(_) => { return 6; },
        Err(e) => {
            match (e) {
                Other(p, msg) => {
                    if (msg != "Operation not permitted") { return 7; }
                },
                _ => { return 8; }
            }
        }
    }

    // A negative pgid: EINVAL.
    match (set_process_group(0, 0 - 1)) {
        Ok(_) => { return 9; },
        Err(e) => {
            match (e) {
                Other(p, msg) => {
                    if (msg != "Invalid argument") { return 10; }
                },
                _ => { return 11; }
            }
        }
    }

    // The group itself. The child moves into a group of its own and sleeps
    // far past anything the parent needs, so the only way it reaches the
    // parent's waitpid is a signal delivered to THAT group — and the parent
    // is not in it, so a kill landing on the caller's own group instead
    // would take the parent down with it.
    var kid: i32 = proc_fork();
    if (kid < 0) { return 12; }
    if (kid == 0) {
        match (set_process_group(0, 0)) {
            Ok(_) => {},
            Err(e) => { exit(71); }
        }
        sleep_ms(60000 as i64);
        exit(70);
    }
    // The parent's half. Both sides make the call so neither ordering leaves
    // the child ungrouped; nothing execs here, so this one succeeds.
    match (set_process_group(kid, kid)) {
        Ok(_) => {},
        Err(e) => { return 13; }
    }
    match (signal_send(0 - kid, 9)) {
        Ok(_) => {},
        Err(e) => { return 14; }
    }
    // proc_waitpid reports a signal death as 128+signal.
    var status: i32 = proc_waitpid(kid);
    if (status != 137) { return 15; }
    return 0;
}`

// setProcessGroupWasmProbe is the refusal probe: set_process_group and nothing
// else, because wasm_unsupported_builtin reports the FIRST unsupported name it
// finds and the measuring probe above forks.
const setProcessGroupWasmProbe = `function main(): i32 {
    match (set_process_group(0, 0)) {
        Ok(_) => { return 0; },
        Err(e) => { return 1; }
    }
}`

// TestSelfHostSetProcessGroupIRX86_64 compiles the probe through the
// production x86-64 IR driver and runs it.
func TestSelfHostSetProcessGroupIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, setProcessGroupSelfHostSource, "-ir")
	if !bytes.Contains(asm, []byte("call __fn___fern_set_process_group")) {
		t.Fatalf("emitted asm has no `call __fn___fern_set_process_group` — set_process_group did not lower through the x86-64 IR path")
	}
	progBin := buildBin(t, gcc, dir, "setpgid_prog", string(asm))
	run := runX86_64Bin(runner, progBin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see setProcessGroupSelfHostSource)\n%s", code, out)
	}
}

// TestSelfHostSetProcessGroupIRArm64 runs the same probe through the arm64 IR
// backend under qemu, where the helper's syscall number is asm-generic 154.
func TestSelfHostSetProcessGroupIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, setProcessGroupSelfHostSource, "-target", "arm64-linux", "-ir")
	if !bytes.Contains(asm, []byte("bl __fn___fern_set_process_group")) {
		t.Fatalf("emitted asm has no `bl __fn___fern_set_process_group` — set_process_group did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "setpgid_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the step (see setProcessGroupSelfHostSource)\n%s", code, out)
	}
}

// TestSelfHostSetProcessGroupIRWasmRefused pins the wasm answer, which is a
// refusal NAMING THE BUILTIN. A component has no process table, so
// platforms.fern withholds `proc`; the wasm driver has to say so itself,
// because instruction selection downstream sees only an IR op and would frame
// a deliberate gap as a missing lowering.
func TestSelfHostSetProcessGroupIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin, "-ir")
	cmd.Stdin = strings.NewReader(setProcessGroupWasmProbe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("wasm driver accepted set_process_group; a component has no process table")
	}
	if !strings.Contains(stderr.String(), "set_process_group is not supported on the wasm target") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
