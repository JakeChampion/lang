package wasmbin

import (
	"strconv"
	"testing"
	"time"
)

// sleep_ms was the last entry in providedMissingLowering: it type-checked
// and passed E066, then died in emitOp with `unknown callee "sleep_ms"`, so
// no program that slept could be built for wasm at all (#7947).
//
// The assertion is elapsed time, not just a successful build, because the
// failure mode this guards is a helper that returns promptly — a
// subscription the host rejects, or a timeout written at the wrong offset,
// both of which leave a module that runs fine and does not sleep. Two
// clocks are read for that: the module's own monotonic delta, which
// isolates the sleep, and the harness's wall time, which catches a delta
// that is right for the wrong reason.
func TestFsSleepMsBlocks(t *testing.T) {
	src := `function main(): i32 {
    var a: i64 = monotonic_ns();
    sleep_ms(120);
    var b: i64 = monotonic_ns();
    return ((b - a) / (1000000 as i64)) as i32;
}`
	start := time.Now()
	got := runFsDirProgram(t, src)
	elapsed := time.Since(start)

	ms, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("main returned %q, want a millisecond count: %v", got, err)
	}
	if ms < 100 {
		t.Errorf("monotonic delta across sleep_ms(120) = %d ms, want >= 100", ms)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("the run took %v, so nothing actually blocked", elapsed)
	}
}
