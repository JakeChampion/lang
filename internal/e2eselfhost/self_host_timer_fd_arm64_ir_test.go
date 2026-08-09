package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostTimerFdIRArm64 is the arm64 half of slice 3 (docs/ASYNC-SELFHOST-IR.md):
// timer_fd / wasm_timer_pollable / wasm_pollable_drop now lower to dedicated IR
// ops on the self-host arm64 IR backend, emitting `bl __fn___fern_timer_fd` (the
// timerfd_create/settime #85/#86 mirror), `bl __fern_wasm_timer_pollable` (-1),
// and `bl __fern_wasm_pollable_drop` (0). A module using them is IR-eligible
// rather than falling back to the AST emitter (which can't emit them).
//
// Same two programs as the x86-64 sibling: (a) a 1 ms timerfd polled with a
// 500 ms budget returns index 0 (also exercises slice-2 arm64 poll over a REAL
// fd), and (b) the portability shims compose to a known exit code. Runs under
// qemu (CI-gated). An asm-content check confirms the timerfd syscall body.
func TestSelfHostTimerFdIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name      string
		src       string
		exit      int
		asmNeedle string
	}{
		{"timerfd-ready", `function main(): i32 {
    var fd: i32 = timer_fd(1);
    var fds: i32[] = [fd];
    return poll(fds, 500);
}`, 0, "bl __fn___fern_timer_fd"},
		{"shims", `function main(): i32 {
    var p: i32 = wasm_timer_pollable(0);
    var d: i32 = wasm_pollable_drop(p);
    return d - p;
}`, 1, "bl __fern_wasm_timer_pollable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), tc.asmNeedle) {
				t.Errorf("%s: emitted arm64 asm missing %q (did not lower through the IR path)", tc.name, tc.asmNeedle)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tfd_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
