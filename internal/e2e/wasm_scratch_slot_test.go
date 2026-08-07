package e2e

// The wasm backend's reserved low-memory scratch slots must not overlap
// (#6229).
//
// Four addresses in wasmbin's hand-picked scratch window were claimed by two
// consumers each, and `print` was the writer on three of them: its iovec sat
// on the rc-underflow counter and the heap-base seed, and its fd_write
// nwritten slot sat on the append-cliff counter. So on wasm, after ANY print:
//
//   - `__heap_bump_bytes()` returned (cursor − the byte length just printed)
//     instead of (cursor − heap base) — i.e. roughly the whole static region,
//     not the bytes the program allocated. docs/LOCAL-DEV-LOOP.md names it as
//     *the* way to measure a Fern program's memory and as the right gate for
//     a memory-regression test; on wasm it was neither.
//   - `__rc_underflow_count()` returned an iov_base pointer, and
//     `__arr_push_shared_count()` an nwritten. Both are assertion-grade
//     diagnostics whose contract is "0 on a healthy run", which is the only
//     thing that makes them usable in a test.
//
// None of it surfaced, because the diagnostics are asserted in programs that
// do not print and nothing both printed and measured. That is what this
// closes: it prints FIRST and reads the diagnostics afterwards, which is the
// exact order the collisions needed.
//
// The fourth pair — the cached instance-network borrow against the cliff's
// weight accumulator — needs a socket to observe, so it is not asserted here.
// It is covered structurally instead: every slot address is now derived from
// the previous slot's end, so a second claimant is unspellable rather than
// merely absent.

import "testing"

// Print, THEN read every diagnostic. The natives implement the same entry
// points over BSS globals, so the same program pins all three backends
// against each other; the interpreter has none of these probes.
//
// `eprint` rather than `print` only so the wasm harness — which reads the
// return value off stdout — is not fed the printed line. It is the same
// helper: buildPrintBodyFd builds print / write / eprint alike, over the same
// iovec and nwritten slots, with fd the only difference.
const wasmScratchSlotProg = `
function main(): i32 {
    var before: i64 = __heap_bump_bytes();
    // Long enough that a clobbered iov_len / nwritten is unmistakably not 0,
    // and heap-form rather than SSO-inline so the helper actually allocates.
    eprint("the reserved low-memory scratch window must not be double-claimed");
    var after: i64 = __heap_bump_bytes();

    // The bump high-water mark can only grow, and one print grows it by the
    // buffer it allocated — not by the whole static region. Reading the
    // DELTA keeps this independent of where the heap happens to start.
    if (after < before) { return 1; }
    if (after - before > 1024) { return 2; }

    // Both counters are "0 on a healthy run"; this run is healthy.
    if (__rc_underflow_count() != 0) { return 3; }
    if (__arr_push_shared_count() != 0) { return 4; }
    var weight: i64 = __arr_push_shared_bytes();
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
