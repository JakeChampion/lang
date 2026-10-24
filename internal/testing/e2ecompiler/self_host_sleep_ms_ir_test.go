package e2ecompiler

import (
	"strings"
	"testing"
)

var sleepMsIRCases = []struct {
	name, src string
	exit      int
}{
	{"basic", `function main(): i32 { sleep_ms(1); return 5; }`, 5},
	{"zero-noop", `function main(): i32 { sleep_ms(0); return 9; }`, 9},
	{"negative-noop", `function main(): i32 { sleep_ms(0 - 3); return 4; }`, 4},
	{"computed-arg", `function ms(): i32 { return 1; } function main(): i32 { sleep_ms(ms() as i64); return 7; }`, 7},
}

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

func TestSelfHostSleepMsIRWasm(t *testing.T) {
	cli := newStrictCLI(t)

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
