package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// `priority` / `set_priority` (#8375's primitive) through the SELF-HOST IR
// path, on each backend that emits them. The read takes no operands and
// pushes an i32 — asmcore.rt_src_priority, reached as
// `call __fn___fern_priority`; the write takes one and pushes
// Result[void, IoError] through rt_src_set_priority.
//
// The probe reads back a value it SET rather than comparing against a range,
// because every way the helpers can be wrong produces a plausible number
// otherwise:
//
//   - Linux returns the value BIASED by 20, so forwarding the syscall's
//     answer unchanged says 20 - want,
//   - applying the correction twice says want - 20,
//   - the wrong syscall number is the OTHER half of the pair, since x86-64
//     numbers get/set 140/141 and the asm-generic table aarch64 uses numbers
//     them 140/141 the other way round — and a `getpriority` that ran
//     `setpriority` answers 0 while quietly reniced,
//   - pushing nothing leaves whatever was already on the stack.
//
// Reading back a value the probe SET is what catches all four, and it keeps
// the test off the ABI split: Linux answers getpriority biased by 20 and BSD
// answers it directly, so an expected value computed in Go would be wrong on
// one of them.
//
// The write is exercised against two measured kernel behaviours: a value
// outside -20..19 is CLAMPED rather than refused, and LOWERING needs
// privilege, so the last leg accepts either outcome as long as a refusal
// moved nothing.
//
// wasm has no leg beyond a refusal: neither preview has a scheduler knob, so
// platforms.fern withholds both builtins on `sched`.
func prioritySelfHostSource() string {
	return `function main(): i32 {
    var orig: i32 = priority();
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

// TestSelfHostPriorityIRX86_64 compiles the probe through the production
// x86-64 IR driver and runs it at a nice value this test set.
func TestSelfHostPriorityIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, prioritySelfHostSource(), "-ir")
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

// TestSelfHostPriorityIRArm64 runs the same probe through the arm64 IR
// backend under qemu, where the pair is asm-generic 141 (get) and 140 (set) —
// the opposite order to x86-64's, which is exactly what a copied table would
// get wrong. qemu-user passes the process's nice value through unchanged, so
// the one set outside it is the one the guest reads.
func TestSelfHostPriorityIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, prioritySelfHostSource(), "-target", "arm64-linux", "-ir")
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

// TestSelfHostPriorityIRWasmRefused pins the wasm answer, which is a refusal
// NAMING THE BUILTIN rather than a fabricated value: answering 0 from
// `priority` would claim the default nice value was measured, and letting
// `set_priority` succeed would claim a change that did not happen.
func TestSelfHostPriorityIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin, "-ir")
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
	if !strings.Contains(stderr.String(), "priority is not supported on the wasm target") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
