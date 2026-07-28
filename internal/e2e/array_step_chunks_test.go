package e2e

// Differential coverage for the std/array adapters `step_by` and
// `chunks_exact`. `step_by(n)` keeps every n-th element from index 0 (empty
// for n <= 0). `chunks_exact(size)` is `chunks` restricted to full-length
// groups — the short trailing remainder is dropped, so every group has
// exactly `size` elements (what fixed-record / SIMD-style loops want). Each
// group is a fresh slice, not a reused loop-local buffer, which the self-host
// RC reuse pass miscompiles (all groups would alias the last). Returns 42 iff
// every check holds across interp / x86-64 / wasm / arm64.

import "testing"

const arrayStepChunksProg = `
import "std/array";
function main(): i32 {
    var xs: i32[] = [10, 20, 30, 40, 50, 60, 70];
    // step_by.
    var s: i32[] = xs.step_by(3);
    if (s.len() != 3 || s[0] != 10 || s[1] != 40 || s[2] != 70) { return 1; }
    if (xs.step_by(1).len() != 7) { return 2; }
    if (xs.step_by(0).len() != 0) { return 3; }
    if (xs.step_by(0 - 2).len() != 0) { return 4; }
    if (xs.step_by(100).len() != 1) { return 5; }
    // chunks_exact — full groups only, remainder dropped.
    var ce: i32[][] = xs.chunks_exact(3);
    if (ce.len() != 2) { return 6; }
    if ((ce[0]).len() != 3 || (ce[0])[0] != 10 || (ce[1])[2] != 60) { return 7; }
    if (xs.chunks_exact(10).len() != 0) { return 8; }
    if (xs.chunks_exact(0).len() != 0) { return 9; }
    // Exact fit: no remainder to drop.
    var ys: i32[] = [1, 2, 3, 4];
    var yce: i32[][] = ys.chunks_exact(2);
    if (yce.len() != 2 || (yce[1])[1] != 4) { return 10; }
    // Every chunks_exact group has exactly the requested size.
    var k: i32 = 0;
    while (k < ce.len()) { if ((ce[k]).len() != 3) { return 11; } k = k + 1; }
    return 42;
}
`

func TestArrayStepChunksInterp(t *testing.T) {
	if got := runInterpExit(t, arrayStepChunksProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayStepChunksX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayStepChunksProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayStepChunksWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayStepChunksProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayStepChunksArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayStepChunksProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
