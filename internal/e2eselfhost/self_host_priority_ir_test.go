package e2eselfhost

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// `priority` / `set_priority` (#8375's primitive) through the SELF-HOST IR
// path, on each backend that emits them. The read takes no operands and
// pushes an i32 — asmcore.rt_src_priority, reached as
// `call __fn___fern_priority`; the write takes one and pushes
// Result[void, IoError] through rt_src_set_priority.
//
// The probe compares against a nice value the HARNESS set on the child, not
// against a range, because every way the helpers can be wrong produces a
// plausible number otherwise:
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
// The write is exercised against two measured kernel behaviours: a value
// outside -20..19 is CLAMPED rather than refused, and LOWERING needs
// privilege, so the last leg accepts either outcome as long as a refusal
// moved nothing.
//
// wasm has no leg beyond a refusal: neither preview has a scheduler knob, so
// platforms.fern withholds both builtins on `sched`.
func prioritySelfHostSource(want int) string {
	return fmt.Sprintf(`function main(): i32 {
    var orig: i32 = priority();
    if (orig != %d) { return 1; }
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
}`, want)
}

// niceProbeValue is a nice value this test imposes on the probe: one the test
// process itself is not running at, so reading the parent's value instead of
// the child's own fails rather than coincides, and one with headroom below 19
// so the `set_priority(19)` leg really is a change.
func niceProbeValue(t *testing.T) int {
	t.Helper()
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatalf("getpriority: %v", err)
	}
	self := 20 - raw
	for _, want := range []int{7, 5, 11, 3} {
		if want != self {
			return want
		}
	}
	t.Fatalf("no probe nice value away from the test process's own %d", self)
	return 0
}

// runAtNice runs argv with the child's nice value set to `want`. Raising is
// always permitted, so this needs no privilege as long as `want` is above the
// value the test process runs at — which is why niceProbeValue picks a small
// positive one. `exec` keeps the shell from adding a process between the
// renice and the probe.
func runAtNice(want int, argv ...string) *exec.Cmd {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return exec.Command("/bin/sh", "-c", fmt.Sprintf("renice -n %d $$ >/dev/null 2>&1; exec %s", want, strings.Join(quoted, " ")))
}

// TestSelfHostPriorityIRX86_64 compiles the probe through the production
// x86-64 IR driver and runs it at a nice value this test set.
func TestSelfHostPriorityIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	want := niceProbeValue(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, prioritySelfHostSource(want), "-ir")
	for _, call := range []string{"call __fn___fern_priority", "call __fn___fern_set_priority"} {
		if !bytes.Contains(asm, []byte(call)) {
			t.Fatalf("emitted asm has no `%s` — the pair did not lower through the x86-64 IR path", call)
		}
	}
	progBin := buildBin(t, gcc, dir, "priority_prog", string(asm))
	run := runAtNice(want, append(append([]string{}, runner...), progBin)...)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (1 = disagreed with the nice %d this test imposed)\n%s", code, want, out)
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
	want := niceProbeValue(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, prioritySelfHostSource(want), "-target", "arm64-linux", "-ir")
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
	run := runAtNice(want, argv...)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (1 = disagreed with the nice %d this test imposed)\n%s", code, want, out)
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
	cmd.Stdin = strings.NewReader(prioritySelfHostSource(0))
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
