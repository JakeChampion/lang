// Memory-boundedness guard for the #4838 CI OOM: a RETURN-position append
// on a threaded accumulator param (`return acc.append(v)`, the self-host
// compiler's AST-walker shape) must stay on the rc-gated in-place grow —
// NOT the #4827 forced-copy path, which copies the whole accumulated
// array per append: O(N²) bump bytes that the leak-mode arena never
// reclaims. That quadratic regime pushed the self-host per-module emits
// past the 8 GiB bump-arena ceiling (exit 137), and the recursive
// split-and-retry on top of it OOM-killed the 16 GB CI runners (exit 143)
// on every PR. inPlacePushes (internal/ir) is the exemption under test;
// TestAppendForcedCopyExemptions pins it at the IR layer, this pins the
// end-to-end heap behaviour.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// appendReturnPosBumpSrc threads an accumulator through a return-position
// append N times and returns the heap-bump high-water / 256. The
// early-return branch makes the append operand a NON-last occurrence of
// `acc` (the walker shape — without it, isLast alone keeps the in-place
// path and the regression is invisible). Measured: O(N) regime 832 →
// 1616 bytes (N=60 → 120, /256 = 3 → 6); forced-copy regime 15856 →
// 60496 (/256 = 61 → 236) — all four fit an 8-bit exit code, and the
// quadratic pair trips assertSubQuadratic's 3x bound.
func appendReturnPosBumpSrc(n string) string {
	return `function step(acc: i32[], v: i32): i32[] {
    if (v >= 0) { return acc.append(v); }
    return acc;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < ` + n + `) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != ` + n + `) { return 255; }
    return ((__heap_bump_bytes() as i32) - before) / 256;
}`
}

func TestX86_64AppendReturnPosBounded(t *testing.T) {
	_, n1 := compileAndRunX86_64FreeOn(t, appendReturnPosBumpSrc("60"))
	_, n2 := compileAndRunX86_64FreeOn(t, appendReturnPosBumpSrc("120"))
	assertSubQuadratic(t, "x86-64-linux", n1, n2)
}

func TestWASMAppendReturnPosBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	n1 := runWasm(t, appendReturnPosBumpSrc("60"))
	n2 := runWasm(t, appendReturnPosBumpSrc("120"))
	assertSubQuadratic(t, "wasm32-wasi", n1, n2)
}
