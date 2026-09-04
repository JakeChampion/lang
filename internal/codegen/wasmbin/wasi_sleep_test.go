package wasmbin

import (
	"strconv"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
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
	// The other direction, which the lower bound cannot see: a timeout
	// scaled by 1e9 instead of 1e6 sleeps for two minutes and still
	// passes every assertion above. The bound is generous enough that
	// only an order-of-magnitude error trips it.
	if elapsed > 30*time.Second {
		t.Errorf("the run took %v, so sleep_ms overslept by an order of magnitude", elapsed)
	}
}

// The preview-2 sleep is a different body: subscribe-duration mints a
// timer pollable, block waits on it, and the drop returns the handle.
// A preview-2 core module imports component-model functions, so it is
// not runnable standalone here — what is checkable is the composition,
// and the hazard worth a gate is losing the DROP, which leaves a module
// that sleeps correctly and exhausts the host's resource table in a
// loop. The preopen cache in wasi_fs_dir.go exists because that already
// happened once.
func TestBuildPreview2WASISleepComposesAndDrops(t *testing.T) {
	src := `
function main(): i32 {
    sleep_ms(1);
    return 0;
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, BuildOptions{Preview2WASI: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []struct{ module, name string }{
		{"wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration"},
		{"wasi:io/poll@0.2.0", "[method]pollable.block"},
		{"wasi:io/poll@0.2.0", "[resource-drop]pollable"},
	} {
		if !importExists(t, bin, want.module, want.name) {
			t.Errorf("preview-2 sleep_ms module missing %s::%s", want.module, want.name)
		}
	}
	if importExists(t, bin, "wasi_snapshot_preview1", "poll_oneoff") {
		t.Errorf("preview-2 module still imports preview-1 poll_oneoff")
	}
}
