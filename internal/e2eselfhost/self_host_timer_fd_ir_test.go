package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostTimerFdIRX86_64 is slice 3 of putting async on the self-hosted
// compiler's IR path (docs/ASYNC-SELFHOST-IR.md): the timer / pollable-lifecycle
// readiness builtins now lower to dedicated IR ops on the self-host x86-64 IR
// backend, so a module using them is IR-ELIGIBLE rather than falling back to the
// AST emitter (which can't emit them):
//
//   - timer_fd(ms)             -> __fern_timer_fd: a CLOCK_MONOTONIC one-shot
//     timerfd readable after `ms` ms.
//   - wasm_timer_pollable(ns)  -> __fern_wasm_timer_pollable: -1 on native (a
//     deadline rides poll(2)'s timeout arg).
//   - wasm_pollable_drop(p)    -> __fern_wasm_pollable_drop: 0 on native (a
//     pollable is just an fd; nothing to drop).
//   - wasm_poll(pollables)     -> __fern_wasm_poll: -1 on native (no real
//     pollables; readiness rides poll(2) directly). On wasm this is the real
//     wasi:io/poll.poll(list<pollable>) multiplexer.
//
// Two programs: (a) a with_deadline-shape readiness case — arm a 1 ms timerfd,
// poll it with a 500 ms budget, and expect index 0 (the timer fires) — which
// also exercises slice-1 poll over a REAL fd; (b) the portability shims, whose
// native values (-1 / 0) compose to a known exit code without any syscall.
func TestSelfHostTimerFdIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A 1 ms one-shot timerfd is ready well within the 500 ms poll budget, so
		// poll returns its index (0). Exercises timer_fd + a real-fd poll.
		{"timerfd-ready", `function main(): i32 {
    var fd: i32 = timer_fd(1);
    var fds: i32[] = [fd];
    return poll(fds, 500);
}`, 0},
		// wasm_timer_pollable(0) == -1, wasm_pollable_drop(-1) == 0 on native, so
		// drop - pollable == 0 - (-1) == 1. No syscall; pins both shims' values.
		{"shims", `function main(): i32 {
    var p: i32 = wasm_timer_pollable(0);
    var d: i32 = wasm_pollable_drop(p);
    return d - p;
}`, 1},
		// wasm_poll over a one-pollable array: -1 on native (no real pollables),
		// and wasm_timer_pollable(0) is also -1, so idx - p == -1 - (-1) == 0. Pins
		// the wasm_poll shim + its i32[]-arg lowering on the register IR path.
		{"wasm-poll-shim", `function main(): i32 {
    var p: i32 = wasm_timer_pollable(0);
    var ps: i32[] = [p];
    var idx: i32 = wasm_poll(ps);
    return idx - p;
}`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "tfd_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
