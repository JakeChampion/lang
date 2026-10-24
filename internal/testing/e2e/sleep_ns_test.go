package e2e

import (
	"testing"
)

const sleepNsSource = `function main(): i32 {
    // Sub-millisecond, the interval sleep_ms has to round up to a full tick.
    let t0: i64 = monotonic_ns();
    sleep_ns(200000 as i64);
    let d0: i64 = monotonic_ns() - t0;
    if (d0 < (200000 as i64)) { return 1; }

    // A whole-millisecond interval crosses the seconds/nanoseconds split the
    // same way, and pins that the divisor is 1e9 rather than 1e3: 30 ms
    // divided by 1e3 would be 30000 seconds.
    let t1: i64 = monotonic_ns();
    sleep_ns(30000000 as i64);
    let d1: i64 = monotonic_ns() - t1;
    if (d1 < (30000000 as i64)) { return 2; }

    // Non-positive returns without entering the kernel.
    let zero: i64 = 0 as i64;
    let neg: i64 = zero - (5 as i64);
    let t2: i64 = monotonic_ns();
    sleep_ns(zero);
    sleep_ns(neg);
    let d2: i64 = monotonic_ns() - t2;
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

func TestInterpSleepNs(t *testing.T) {
	if code := runInterpExit(t, sleepNsSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see sleepNsSource)", code)
	}
}

func TestWASMPreview1SleepNs(t *testing.T) {
	if got := runWasm(t, sleepNsSource); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see sleepNsSource)", got)
	}
}

func TestWASMSleepNs(t *testing.T) {
	if got := runWasmResult(t, sleepNsSource); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see sleepNsSource)", got)
	}
}
