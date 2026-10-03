package e2eselfhost

import (
	"strings"
	"testing"
)

// sleepMsIRCases exercise the `sleep_ms(ms)` builtin through the IR path. It
// lowers to a dedicated `sleep_ms` IR op (a void-with-drop op, like putchar)
// that the x86-64 / arm64 backends emit as a call into the Fern-compiled
// __fn___fern_sleep_ms nanosleep helper. A 1 ms sleep is fast and
// observable only by exit code, so the program just returns a sentinel after
// sleeping — proving the op lowered and the helper linked. The wasm leg has its
// own timed program below.
var sleepMsIRCases = []struct {
	name, src string
	exit      int
}{
	{"basic", `function main(): i32 { sleep_ms(1); return 5; }`, 5},
	{"zero-noop", `function main(): i32 { sleep_ms(0); return 9; }`, 9},
	{"negative-noop", `function main(): i32 { sleep_ms(0 - 3); return 4; }`, 4},
	{"computed-arg", `function ms(): i32 { return 1; } function main(): i32 { sleep_ms(ms() as i64); return 7; }`, 7},
}

// TestSelfHostSleepMsIRX86_64 compiles and runs each case to its exit code,
// with the emitted asm calling __fn___fern_sleep_ms.
func TestSelfHostSleepMsIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range sleepMsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src)
			if !strings.Contains(asm, "call __fn___fern_sleep_ms") {
				t.Fatalf("%s: emitted asm has no `call __fn___fern_sleep_ms`", tc.name)
			}
			if code, _ := cli.runX86(t, asm); code != tc.exit {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostSleepMsIRArm64 runs the same cases through the arm64 backend
// under qemu (CI-gated). The arm64 op handler emits `bl __fn___fern_sleep_ms`.
func TestSelfHostSleepMsIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range sleepMsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "arm64-linux", tc.src)
			if !strings.Contains(asm, "bl __fn___fern_sleep_ms") {
				t.Fatalf("%s: emitted asm has no `bl __fn___fern_sleep_ms` — sleep_ms did not lower through the arm64 IR path", tc.name)
			}
			if code, _ := runArm64(t, gcc, qemu, asm); code != tc.exit {
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
	cli := newStrictCLI(t)

	// 50 ms sleep; require >= 40 ms elapsed (40_000_000 ns) so a no-op "sleep"
	// fails. No upper bound — a slow runner sleeping longer is still correct.
	const src = `function main(): i32 {
    let t0: i64 = monotonic_ns();
    sleep_ms(50);
    let t1: i64 = monotonic_ns();
    let elapsed: i64 = t1 - t0;
    if (elapsed < 40000000) { return 1; }
    return 0;
}`

	wat := cli.emit(t, "wasm32-wasi", src)
	if !strings.Contains(wat, "call $__fern_sleep_ms") {
		t.Fatal("sleep_ms did not reach the wasm IR runtime path (no call $__fern_sleep_ms in WAT)")
	}
	if code, _ := runWasm(t, wat); code != 0 {
		t.Errorf("sleep_ms wasm IR program exited %d, want 0 (>= 40ms should have elapsed)\n--- WAT ---\n%s", code, wat)
	}
}
