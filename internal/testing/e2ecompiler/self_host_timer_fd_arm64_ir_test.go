package e2ecompiler

import (
	"strings"
	"testing"
)

// Native ARM64 timer_fd creates a real timer descriptor; the portable WASI
// adapters select constants. Check both a timer becoming ready and the
// constant values composing correctly. The runtime calls are absent from
// emitted assembly. Execution runs on native ARM64 CI or under qemu.
func TestSelfHostTimerFdIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name      string
		src       string
		exit      int
		asmNeedle string
	}{
		{"timerfd-ready", `function main(): i32 {
    let fd: i32 = timer_fd(1);
    let fds: i32[] = [fd];
    return poll(fds, 500);
}`, 0, "bl __fn___fern_timer_fd"},
		{"shims", `function main(): i32 {
    let p: i32 = wasm_timer_pollable(0);
    let d: i32 = wasm_pollable_drop(p);
    return d - p;
}`, 1, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if tc.asmNeedle != "" && !strings.Contains(string(asm), tc.asmNeedle) {
				t.Errorf("%s: emitted arm64 asm missing %q (did not lower through the IR path)", tc.name, tc.asmNeedle)
			}
			for _, name := range []string{"wasm_timer_pollable", "wasm_pollable_drop", "wasm_block", "wasm_poll"} {
				if strings.Contains(string(asm), "__fern_"+name) {
					t.Errorf("native constant adapter %s still has a runtime helper", name)
				}
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
