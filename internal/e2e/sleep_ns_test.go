// `sleep_ns` (#8528) end to end on every backend, alongside the `sleep_ms` it
// refines.
//
// What no unit test can assert: each backend builds the wait by hand and each
// one rounds somewhere different — x86-64 and arm64-linux divide a timespec by
// 1e9, arm64-darwin rounds up into `select`'s microsecond timeval because
// there is no nanosleep syscall there, and both WASI previews take the
// nanoseconds verbatim. A helper that reused `sleep_ms`'s 1e3 divisor, or
// stored the remainder in the wrong timespec word, still sleeps; it just
// sleeps for the wrong length. So the probe measures.
//
// Every bound is ONE-SIDED. nanosleep may overshoot by any amount the
// scheduler likes, so "not shorter than asked" is the only property the helper
// owns; an upper bound would be a test of the machine's load.
package e2e

import (
	"os"
	"testing"
)

// sleepNsSource returns a non-zero code per failing step.
//
// The 200 µs step is the one that justifies the primitive: `sleep_ms` cannot
// express it at all, and a backend that divided by 1e3 instead of 1e9 would
// pause for 200 SECONDS rather than fail, which is why the suite would notice.
const sleepNsSource = `function main(): i32 {
    // Sub-millisecond, the interval sleep_ms has to round up to a full tick.
    var t0: i64 = monotonic_ns();
    sleep_ns(200000 as i64);
    var d0: i64 = monotonic_ns() - t0;
    if (d0 < (200000 as i64)) { return 1; }

    // A whole-millisecond interval crosses the seconds/nanoseconds split the
    // same way, and pins that the divisor is 1e9 rather than 1e3: 30 ms
    // divided by 1e3 would be 30000 seconds.
    var t1: i64 = monotonic_ns();
    sleep_ns(30000000 as i64);
    var d1: i64 = monotonic_ns() - t1;
    if (d1 < (30000000 as i64)) { return 2; }

    // Non-positive returns without entering the kernel.
    var zero: i64 = 0 as i64;
    var neg: i64 = zero - (5 as i64);
    var t2: i64 = monotonic_ns();
    sleep_ns(zero);
    sleep_ns(neg);
    var d2: i64 = monotonic_ns() - t2;
    if (d2 > (500000000 as i64)) { return 3; }
    return 0;
}
`

func TestX86_64SleepNs(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, sleepNsSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see sleepNsSource)\n%s", code, out)
	}
}

func TestArm64SleepNs(t *testing.T) {
	out, code := compileAndRunArm64(t, sleepNsSource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see sleepNsSource)\n%s", code, out)
	}
}

// The SSA-direct arm64 backend writes its own timespec in its own frame
// discipline, so it gets the probe rather than being taken on trust.
func TestArm64SSASleepNs(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, sleepNsSource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see sleepNsSource)\n%s", code, stderr)
	}
}

func TestInterpSleepNs(t *testing.T) {
	if code := runInterpExit(t, sleepNsSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see sleepNsSource)", code)
	}
}

// wasm is the one target that honours the full resolution without rounding:
// preview-1's poll_oneoff timeout and preview-2's subscribe-duration are both
// already nanoseconds. main's return reaches us on stdout, not as the exit
// status — the harness builds with PrintMainResult.
func TestWASMSleepNs(t *testing.T) {
	p := buildComponent(t, sleepNsSource)
	stdout, stderr, ec := runComponent(t, p, runOpts{})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see sleepNsSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
}
