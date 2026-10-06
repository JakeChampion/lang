package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// `clock_resolution` / `clock_set` (#9166) through the SELF-HOST IR path, on
// each backend that emits them: asmcore.rt_src_clock_resolution reached as
// `call __fn___fern_clock_resolution`, and rt_src_clock_set in the io_error
// bundle as `call __fn___fern_clock_set`.
//
// The resolution is compared with the host's clock_getres, which qemu-user
// and wasmtime both pass through. The set probe runs without the privilege
// to move the clock (e2eharness.WithoutClockPrivilege), asks for the current
// time, and expects EPERM; the two out-of-range nanosecond counts are EINVAL
// at any privilege. The current seconds are far past 999999999, so operands
// that arrived swapped would answer EINVAL to the first call too.
func clockSelfHostSource(t *testing.T) string {
	return fmt.Sprintf(`function refused(r: Result[void, IoError], want: string): boolean {
    match (r) {
        Ok(_) => { return false; },
        Err(e) => {
            match (e) {
                Other(_, msg, _) => { return msg == want; },
                _ => { return false; }
            }
        }
    }
}

function main(): i32 {
    let res: i64 = %d;
    if (clock_resolution() != res) { return 2; }
    let billion: i64 = 1000000000;
    let now: i64 = now_ns();
    if (!refused(clock_set(now / billion, now %% billion), "Operation not permitted")) { return 3; }
    if (!refused(clock_set(now / billion, billion), "Invalid argument")) { return 4; }
    if (!refused(clock_set(now / billion, 0 - 1), "Invalid argument")) { return 5; }
    return 0;
}`, linuxClockResolution(t))
}

// linuxClockResolution is the clock_getres the probes' Linux binaries see,
// which is the host's only when the host is that Linux.
func linuxClockResolution(t *testing.T) int64 {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("the probes compare with a Linux host's clock_getres; this is %s", runtime.GOOS)
	}
	return e2eharness.HostClockResolution(t)
}

const clockSelfHostExits = "2 = clock_resolution is not the host's clock_getres, 3 = no EPERM for the current time, 4/5 = no EINVAL for an out-of-range nsec"

func runClockProbe(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	e2eharness.WithoutClockPrivilege(t, cmd)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (%s)\n%s", code, clockSelfHostExits, out)
	}
}

// TestSelfHostClockSettimeIRX86_64 compiles the probe through the production
// x86-64 IR driver.
func TestSelfHostClockSettimeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, clockSelfHostSource(t))
	for _, call := range []string{"call __fn___fern_clock_resolution", "call __fn___fern_clock_set"} {
		if !bytes.Contains(asm, []byte(call)) {
			t.Fatalf("emitted asm has no `%s` — the pair did not lower through the x86-64 IR path", call)
		}
	}
	runClockProbe(t, runX86_64Bin(runner, buildBin(t, gcc, dir, "clock_prog", string(asm))))
}

// TestSelfHostClockSettimeIRArm64 is the same probe through the arm64 IR
// backend under qemu, where the pair is asm-generic 112 (settime) and 114
// (getres) rather than x86-64's 227 and 229.
func TestSelfHostClockSettimeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, clockSelfHostSource(t), "-target", "arm64-linux")
	for _, call := range []string{"bl __fn___fern_clock_resolution", "bl __fn___fern_clock_set"} {
		if !bytes.Contains(asm, []byte(call)) {
			t.Fatalf("emitted asm has no `%s` — the pair did not lower through the arm64 IR path", call)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "clock_prog", string(asm))
	argv := []string{bin}
	if qemu != "" {
		argv = []string{qemu, bin}
	}
	runClockProbe(t, exec.Command(argv[0], argv[1:]...))
}

// TestSelfHostClockSettimeIRWasm runs both halves under wasmtime: the
// resolution through preview1 clock_res_get, which answers the host's clock,
// and the set through a body that answers Unsupported, since a wasm host has
// a clock and no call that sets it.
func TestSelfHostClockSettimeIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host clock wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := fmt.Sprintf(`function unsupported(r: Result[void, IoError]): boolean {
    match (r) {
        Ok(_) => { return false; },
        Err(e) => {
            match (e) {
                Unsupported => { return true; },
                _ => { return false; }
            }
        }
    }
}

function main(): i32 {
    let res: i64 = %d;
    if (clock_resolution() != res) { return 2; }
    let billion: i64 = 1000000000;
    let now: i64 = now_ns();
    if (!unsupported(clock_set(now / billion, now %% billion))) { return 3; }
    return 0;
}
`, linuxClockResolution(t))
	wat := runSelfHostDriverStdin(t, runner, driverBin, src)
	for _, call := range []string{"call $__fern_clock_resolution", "call $__fern_clock_set"} {
		if !bytes.Contains(wat, []byte(call)) {
			t.Fatalf("emitted WAT has no `%s` — the pair did not lower through the wasm IR path", call)
		}
	}
	watFile := filepath.Join(dir, "clock.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (2 = clock_resolution is not the host's clock_getres, 3 = clock_set did not answer Unsupported)\n%s", code, out)
	}
}
