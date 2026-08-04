package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Regression for #4376: a string accumulator initialised from a bare string
// LITERAL (`var s = ""`) must be freeEligible so the dec-on-overwrite reclaims
// each intermediate. `rhsTainted` had *ast.NumberLit/FloatLit/BoolLit cases but
// NO *ast.StringLit case, so a plain `""` init fell to `default: return true`
// and tainted the local forever — every reassignment leaked the prior buffer
// (linear bump growth; O(n²) bytes on the growing `s = s + p` edge-handler
// pattern). A Binary init (`var s = "a" + ""`) reclaimed fine, so only the
// literal-init shape was broken. The IR-layer guard (that the overwrite dec is
// emitted, at both pointer widths) lives in internal/ir; this pins the
// end-to-end heap payoff on wasm.
//
// The probe uses a CONSTANT-size overwrite (`s = p + "!"`, p a fixed 20-char
// string) and measures the bump allocator's high-water via __heap_bump_bytes().
// When reclaim works the high-water reaches a bounded steady state and stays
// FLAT as the iteration count grows; a leak instead grows linearly with N. The
// measured behaviour (this exact program) is:
//
//	          N=5000   N=20000  N=80000
//	leak   -> 160000   640000   2560000   (unbounded, ~32 B/iter)
//	fixed  ->  64544    64544     64544    (bounded steady state)
//
// So comparing two large N well past the plateau (20000 vs 80000) is a crisp
// bounded-vs-linear discriminator that is independent of the exact plateau
// value (host-independent: __heap_bump_bytes reads the wasm module's own cursor
// and wasmtime executes it deterministically).
//
// wasm-only: this is the two-word string ABI (__fern_str_dec on overwrite).
// The native x86_64 single-word path also reclaims (rc_dec on overwrite; IR
// guard covers it) but __heap_bump_bytes there reuses so aggressively that the
// net high-water is 0 with or without the fix — not a usable behavioural probe.
// arm64 is intentionally excluded: native-arm64 heap-string reclamation is the
// RC-perceus deferred slice 5g, so the overwrite dec is not emitted there
// (ir.go ~17240) and the accumulator keeps its safe-leak behaviour.
func heapBumpStrLiteralAccumSrc(n string) string {
	return `function main(): i32 {
    var p: string = "0123456789abcdefghij";
    var s: string = "";
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        s = p + "!";
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMHeapBumpStrLiteralAccumBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	// Both N are well past the ~5000-iteration point where the reclaimed
	// high-water plateaus, so a working reclaim gives EQUAL bytes while a leak
	// grows 4x (640000 -> 2560000).
	small := runWasm(t, heapBumpStrLiteralAccumSrc("20000"))
	large := runWasm(t, heapBumpStrLiteralAccumSrc("80000"))
	if small != large {
		t.Errorf("literal-init string accumulator should reclaim (bounded bump): N=20000 -> %d, N=80000 -> %d (a leak grows ~4x with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (the accumulator does heap-allocate), got 0")
	}
}
