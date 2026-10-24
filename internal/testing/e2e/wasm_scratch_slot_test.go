package e2e

import "testing"

const wasmScratchSlotProg = `
function main(): i32 {
    let before: i64 = __heap_bump_bytes();
    // Long enough that a clobbered iov_len / nwritten is unmistakably not 0,
    // and heap-form rather than SSO-inline so the helper actually allocates.
    eprint("the reserved low-memory scratch window must not be double-claimed");
    let after: i64 = __heap_bump_bytes();

    // The bump high-water mark can only grow, and one print grows it by the
    // buffer it allocated — not by the whole static region. Reading the
    // DELTA keeps this independent of where the heap happens to start.
    if (after < before) { return 1; }
    if (after - before > 1024) { return 2; }

    // Both counters are "0 on a healthy run"; this run is healthy.
    if (__rc_underflow_count() != 0) { return 3; }
    if (__arr_push_shared_count() != 0) { return 4; }
    let weight: i64 = __arr_push_shared_bytes();
    if (weight != 0) { return 5; }

    return 42;
}
`

func TestWasmScratchSlotsDontCollideWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, wasmScratchSlotProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (2 = print clobbered the heap-base seed; "+
			"3 = it clobbered the rc-underflow counter; 4 = it clobbered the "+
			"append-cliff counter)", got)
	}
}

func TestWasmScratchSlotsDontCollideX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, wasmScratchSlotProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestWasmScratchSlotsDontCollideArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, wasmScratchSlotProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
