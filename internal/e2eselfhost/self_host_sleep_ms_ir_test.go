package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sleepMsIRCases exercise the `sleep_ms(ms)` builtin through the IR path. It
// lowers to a dedicated `sleep_ms` IR op (a void-with-drop op, like putchar)
// that the x86-64 / arm64 backends emit as a call into the __fern_sleep_ms
// nanosleep helper the AST path already provides. A 1 ms sleep is fast and
// observable only by exit code, so the program just returns a sentinel after
// sleeping — proving the op lowered and the helper linked. wasm is excluded:
// there is no wasm sleep runtime (a WASI poll-based sleep is the deferred
// #2843 item), so a sleep module stays on the wasm AST path.
var sleepMsIRCases = []struct {
	name, src string
	exit      int
}{
	{"basic", `function main(): i32 { sleep_ms(1); return 5; }`, 5},
	{"zero-noop", `function main(): i32 { sleep_ms(0); return 9; }`, 9},
	{"negative-noop", `function main(): i32 { sleep_ms(0 - 3); return 4; }`, 4},
	{"computed-arg", `function ms(): i32 { return 1; } function main(): i32 { sleep_ms(ms()); return 7; }`, 7},
}

// TestSelfHostSleepMsIRX86_64 proves each case (a) routes through the IR path
// (asm_pathprobe_run prints "ir") and (b) compiles + runs to its exit code
// through the production x86-64 driver (asm_run, IR default-on), with the
// emitted asm calling __fern_sleep_ms.
func TestSelfHostSleepMsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range sleepMsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			probe := runCapture(t, gcc, runner, probeBin, []byte(tc.src))
			if strings.TrimSpace(string(probe)) != "ir" {
				t.Fatalf("%s: path probe = %q, want \"ir\" (sleep_ms bailed the module to the AST path)", tc.name, probe)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_sleep_ms")) {
				t.Fatalf("%s: emitted asm has no `call __fern_sleep_ms`", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "sm_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostSleepMsIRArm64 runs the same cases through the arm64 IR backend
// under qemu (CI-gated). The arm64 op handler emits `bl __fern_sleep_ms`, whose
// helper asm_arm64.emit_runtime already provides unconditionally.
func TestSelfHostSleepMsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range sleepMsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_sleep_ms")) {
				t.Fatalf("%s: emitted asm has no `bl __fern_sleep_ms` — sleep_ms did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "sm_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("sleep_ms arm64 IR %q: exit %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostSleepMsIRWasm closes the deferred #2843 item: sleep_ms now lowers on
// the wasm IR path too. wasm has no sleep syscall, but a bare timed block needs
// only preview1 poll_oneoff with a SINGLE monotonic-clock subscription (not the
// reactor's wasi:io/poll multiplexer), so $__fern_sleep_ms is self-contained. The
// op stays void-with-drop, like the register backends.
//
// Where the x86 / arm64 cases above can only observe a 1 ms sleep by exit code,
// this one actually verifies the block happened: it reads monotonic_ns, sleeps
// 50 ms, reads it again, and exits 0 only if >= 40 ms elapsed (a lower-bound
// check — poll_oneoff blocks for >= the timeout, so it is robust to slow runners
// and never flakes high). Pins that the IR path was taken (call $__fern_sleep_ms).
func TestSelfHostSleepMsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sleep_ms wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// 50 ms sleep; require >= 40 ms elapsed (40_000_000 ns) so a no-op "sleep"
	// fails. No upper bound — a slow runner sleeping longer is still correct.
	const src = `function main(): i32 {
    var t0: i64 = monotonic_ns();
    sleep_ms(50);
    var t1: i64 = monotonic_ns();
    var elapsed: i64 = t1 - t0;
    if (elapsed < 40000000) { return 1; }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_sleep_ms")) {
		t.Fatal("sleep_ms did not reach the wasm IR runtime path (no call $__fern_sleep_ms in WAT)")
	}
	watFile := filepath.Join(dir, "sleep_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("sleep_ms wasm IR program exited %d, want 0 (>= 40ms should have elapsed)\n--- WAT ---\n%s", code, wat)
	}
}
