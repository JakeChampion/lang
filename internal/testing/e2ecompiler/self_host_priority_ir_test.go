package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func prioritySelfHostSource() string {
	return `function main(): i32 {
    let orig: i32 = priority();
    match (set_priority(19)) {
        Ok(_) => {},
        Err(_) => { return 2; }
    }
    if (priority() != 19) { return 3; }
    match (set_priority(1000)) {
        Ok(_) => {},
        Err(_) => { return 4; }
    }
    if (priority() != 19) { return 5; }
    match (set_priority(orig)) {
        Ok(_) => {
            if (priority() != orig) { return 6; }
        },
        Err(_) => {
            if (priority() != 19) { return 7; }
        }
    }
    return 0;
}`
}

func TestSelfHostPriorityIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, prioritySelfHostSource())
	for _, call := range []string{"call __fn___fern_priority", "call __fn___fern_set_priority"} {
		if !bytes.Contains(asm, []byte(call)) {
			t.Fatalf("emitted asm has no `%s` — the pair did not lower through the x86-64 IR path", call)
		}
	}
	progBin := buildBin(t, gcc, dir, "priority_prog", string(asm))
	run := runX86_64Bin(runner, progBin)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (3 = the value set was not the value read back)\n%s", code, out)
	}
}

func TestSelfHostPriorityIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, prioritySelfHostSource(), "-target", "arm64-linux")
	for _, call := range []string{"bl __fn___fern_priority", "bl __fn___fern_set_priority"} {
		if !bytes.Contains(asm, []byte(call)) {
			t.Fatalf("emitted asm has no `%s` — the pair did not lower through the arm64 IR path", call)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "priority_prog", string(asm))
	argv := []string{bin}
	if qemu != "" {
		argv = []string{qemu, bin}
	}
	run := exec.Command(argv[0], argv[1:]...)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (3 = the value set was not the value read back)\n%s", code, out)
	}
}

func TestSelfHostPriorityIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin)
	cmd.Stdin = strings.NewReader(prioritySelfHostSource())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("wasm driver accepted the priority pair; it has no scheduler knob")
	}
	if !strings.Contains(stderr.String(), "required by `priority`; targets providing it") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
