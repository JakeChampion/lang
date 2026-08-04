package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Ownership transfer for `own` (consuming) parameters: the caller transfers an
// owned argument into an `own` parameter (no inc, and the caller's drop is
// suppressed) and the callee reclaims it. These churn loops force transfer to
// fire every iteration; the value is correct only if every transfer/drop
// balanced, and __rc_underflow_count() == 0 proves no over-release (no double
// free, no use-after-free). `own` is opt-in and unused elsewhere, so these are
// the primary correctness gate for the feature.

// ownTransferFreshSrc: a fresh array temp is transferred into consume's `own`
// param each iteration; consume owns + drops it (the caller's stage-(b) reclaim
// is suppressed). No leak, no double free.
const ownTransferFreshSrc = `function consume(own xs: i32[]): i32 { return xs[0] + xs[1]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        acc = acc + consume([i, i + 1]);   // [i,i+1] transferred to consume
        i = i + 1;
    }
    // consume = i + (i+1) = 2i+1; sum i=0..299 = 2*44850 + 300 = 90000
    if (acc != 90000) { return 999; }
    return __rc_underflow_count();
}`

// ownTransferChainSrc: a two-hop transfer — a fresh temp into relay's `own ys`,
// then ys onward into consume's `own xs` (move-on-call: relay skips its drop).
// consume is the sole owner and frees it once.
const ownTransferChainSrc = `function consume(own xs: i32[]): i32 { return xs[0] + xs[1]; }
function relay(own ys: i32[]): i32 { return consume(ys); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        acc = acc + relay([i, i + 1]);
        i = i + 1;
    }
    if (acc != 90000) { return 999; }
    return __rc_underflow_count();
}`

var ownTransferCases = []struct{ name, src string }{
	{"fresh", ownTransferFreshSrc},
	{"chain", ownTransferChainSrc},
}

func TestX86_64OwnTransfer(t *testing.T) {
	for _, c := range ownTransferCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64OwnTransfer(t *testing.T) {
	for _, c := range ownTransferCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMOwnTransfer(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range ownTransferCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
	// Heap-bump bound: the transferred array is freed by the callee each turn,
	// so the loop holds a bounded high-water (N=5000 == N=50000) rather than
	// leaking one array per iteration.
	bumpSrc := func(n string) string {
		return `function consume(own xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        var unused: i32 = consume([i, i + 1, i + 2, i + 3]);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bumpSrc("5000"))
	large := runWasm(t, bumpSrc("50000"))
	if small != large {
		t.Errorf("own-transfer should free the arg each turn (bounded): N=5000 -> %d, N=50000 -> %d", small, large)
	}
}
