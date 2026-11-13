package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

// A view that lives in the frame of its function has an immortal count, so
// the register backends emit no release for it (#15748). On wasm a string
// slice is a copy on the heap, and its release stays. With no release after
// it, a call that reads a frame view is in tail position, and a tail call
// would end the frame that holds the view. A tail call that reads no frame
// view stays a tail call.
const frameViewReleaseSrc = `@noinline
function first_space(s: string, e: i32): i32 {
    let view = slice_unchecked(s, 0, e);
    return __memchr(view, 32, 0);
}

@noinline
function space_count(s: string): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < s.len()) {
        if (s[i] == b' ') { n = n + 1; }
        i = i + 1;
    }
    return n;
}

@noinline
function head_spaces(s: string, e: i32): i32 {
    return space_count(slice_unchecked(s, 0, e));
}

@noinline
function first_space_or_count(s: string, e: i32): i32 {
    if (e > s.len()) { return space_count(s); }
    return __memchr(slice_unchecked(s, 0, e), 32, 0);
}

@noinline
function total(v: [u8]): i32 {
    let n: i32 = 0;
    for b in v { n = n + (b as i32); }
    return n;
}

@noinline
function mid_sum(xs: u8[], lo: i32, hi: i32): i32 {
    let view: [u8] = xs[lo:hi];
    return total(view);
}

@noinline
function word(n: i32): string {
    let w: string = "";
    let i: i32 = 0;
    while (i < n) {
        w = w + "a";
        i = i + 1;
    }
    return w;
}

function main(): i32 {
    let s: string = word(3) + " " + word(4);
    let found: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        found = found + first_space(s, 4 + i % 4);
        i = i + 1;
    }
    if (found != 300) { return 1; }
    if (first_space(s, 2) != -1) { return 2; }
    let heads: i32 = 0;
    i = 0;
    while (i < 50) {
        heads = heads + head_spaces(s, 4 + i % 5);
        i = i + 1;
    }
    if (heads != 50) { return 4; }
    if (head_spaces(s, 3) != 0) { return 5; }
    if (first_space_or_count(s, 100) != 1) { return 6; }
    if (first_space_or_count(s, 5) != 3) { return 7; }
    let xs: u8[] = [1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8];
    let t: i32 = 0;
    i = 0;
    while (i < 10) {
        t = t + mid_sum(xs, 1, 4);
        i = i + 1;
    }
    if (t != 90) { return 3; }
    return 42;
}
`

const frameViewReleaseExit = 42

// checkFrameViewRelease requires each view function to build its view in
// the frame, marked by the immortal count, and to release nothing. The call
// that reads the view in head_spaces must return to its frame, and the one
// in first_space_or_count that reads no view must be a tail call.
func checkFrameViewRelease(t *testing.T, asm, immortal, call, tail string) {
	t.Helper()
	if body := leafFunction(t, asm, "head_spaces"); !strings.Contains(body, call) {
		t.Errorf("head_spaces: no %q, so the call that reads the frame view ends the frame:\n%s", call, body)
	}
	if body := leafFunction(t, asm, "first_space_or_count"); !strings.Contains(body, tail) {
		t.Errorf("first_space_or_count: no %q, so a frame view stops a tail call that does not read it:\n%s", tail, body)
	}
	for _, fn := range []string{"first_space", "mid_sum", "head_spaces", "first_space_or_count"} {
		body := leafFunction(t, asm, fn)
		if !strings.Contains(body, immortal) {
			t.Errorf("%s: no view with an immortal count in the frame:\n%s", fn, body)
		}
		for _, release := range []string{"__fern_str_view_free", "__sem_view_release"} {
			if strings.Contains(body, release) {
				t.Errorf("%s: releases its frame view through %s:\n%s", fn, release, body)
			}
		}
	}
}

func TestSelfHostFrameViewReleaseX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	checkFrameViewRelease(t, hevCompile(t, runner, driverBin, frameViewReleaseSrc, nil), "movq $-1, (", "call __fn_space_count.r\n", "jmp __fn_space_count.r\n")
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		asm := hevCompile(t, runner, driverBin, frameViewReleaseSrc, []string{mode})
		bin := buildBin(t, gcc, dir, "frame_view_release", asm)
		stderr, exit := hevRun(t, runner, bin)
		if exit != frameViewReleaseExit {
			t.Fatalf("%s: exit = %d, want %d\n%s", mode, exit, frameViewReleaseExit, stderr)
		}
		assertBalancedCensus(t, stderr)
	}
}

func TestSelfHostFrameViewReleaseArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	compile := func(env ...string) string {
		asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(frameViewReleaseSrc), append([]string{"PATH=/usr/bin:/bin"}, env...), "-target", "arm64-linux"))
		if len(asm) == 0 {
			t.Fatal("self-host arm64 compiler emitted 0 bytes")
		}
		return asm
	}
	checkFrameViewRelease(t, compile(), ", #-1\n", "bl __fn_space_count.r\n", "    b __fn_space_count.r\n")
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		bin := buildBinArm64(t, arm64gcc, dir, "frame_view_release_arm64", compile(mode))
		cmd := runArm64Bin(qemu, bin)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != frameViewReleaseExit {
			t.Fatalf("%s: exit = %d, want %d\n%s", mode, code, frameViewReleaseExit, errBuf.String())
		}
		assertBalancedCensus(t, errBuf.String())
	}
}

// On wasm the string slice is a copy, so the census holds only when its
// release stays.
func TestSelfHostFrameViewReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm frame view release e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasmdriver")
	wat := wasmLcCompile(t, runner, driverBin, frameViewReleaseSrc, []string{"FERN_LEAKCHECK=1"})
	stderr, exit := wasmLcRun(t, dir, "frame_view_release", wat)
	if exit != frameViewReleaseExit {
		t.Fatalf("exit = %d, want %d\n%s", exit, frameViewReleaseExit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
