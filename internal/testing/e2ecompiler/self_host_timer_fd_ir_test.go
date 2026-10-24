package e2ecompiler

import (
	"testing"
)

func TestSelfHostTimerFdIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"timerfd-ready", `function main(): i32 {
    let fd: i32 = timer_fd(1);
    let fds: i32[] = [fd];
    return poll(fds, 500);
}`, 0},
		{"shims", `function main(): i32 {
    let p: i32 = wasm_timer_pollable(0);
    let d: i32 = wasm_pollable_drop(p);
    return d - p;
}`, 1},
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
