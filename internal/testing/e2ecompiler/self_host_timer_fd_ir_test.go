package e2ecompiler

import (
	"testing"
)

// Native timer_fd creates a real timer descriptor. Polling it checks timer
// readiness; the portable WASI operations select their native constants
// without calling a runtime helper.
func TestSelfHostTimerFdIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A 1 ms one-shot timerfd is ready well within the 500 ms poll budget, so
		// poll returns its index (0). Exercises timer_fd + a real-fd poll.
		{"timerfd-ready", `function main(): i32 {
    let fd: i32 = timer_fd(1);
    let fds: i32[] = [fd];
    return poll(fds, 500);
}`, 0},
		// wasm_timer_pollable(0) == -1, wasm_pollable_drop(-1) == 0 on native, so
		// drop - pollable == 0 - (-1) == 1. No syscall; pins both shims' values.
		{"shims", `function main(): i32 {
    let p: i32 = wasm_timer_pollable(0);
    let d: i32 = wasm_pollable_drop(p);
    return d - p;
}`, 1},
		// wasm_poll over a one-pollable array: -1 on native (no real pollables),
		// and wasm_timer_pollable(0) is also -1, so idx - p == -1 - (-1) == 0. Pins
		// the wasm_poll shim + its i32[]-arg lowering on the register IR path.
		{"wasm-poll-shim", `function main(): i32 {
    let p: i32 = wasm_timer_pollable(0);
    let ps: i32[] = [p];
    let idx: i32 = wasm_poll(ps);
    return idx - p;
}`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != tc.exit {
				t.Errorf("%s exited %d, want %d\n%s", tc.name, code, tc.exit, stderr)
			}
		})
	}
}
