package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Wide-scalar (i64 / f64) struct fields are reuse-eligible (#4356
// divergence 1). The self-overwrite rebuild churn is the shape that used to
// fall back to fresh allocs: with the width-stamped reuse temps it now
// reuses the box in place every iteration. These programs are the
// correctness gate — the i64/f64 values must survive the temp round-trip
// (a mis-sized temp truncates i64 to 32 bits or garbles the f64 bits), the
// over-release detector must stay 0, and the loop must hold a bounded
// high-water on wasm (the reuse means zero allocs per iteration).

const wideReuseSrc = `struct Acc { total: i64, scale: f64, n: i32 }
function main(): i32 {
    var p: Acc = Acc { total: 0, scale: 1.0, n: 0 };
    var i: i32 = 0;
    while (i < 1000) {
        p = Acc { total: p.total + 3000000000, scale: p.scale, n: p.n + 1 };
        i = i + 1;
    }
    // 1000 * 3e9 = 3e12 — far past i32 range; a truncated temp cannot
    // reproduce it. Check via division back down to i32 range.
    var q: i64 = p.total / 3000000000;
    if (q != 1000) { return 90; }
    if (p.n != 1000) { return 91; }
    if (p.scale != 1.0) { return 92; }
    // Field SWAP through the wide temps — the read-before-overwrite hazard
    // the temps exist for: both i64 reads must complete (width-correct)
    // before the reused box is overwritten.
    var w: Swap = Swap { a: 6000000000, b: 7000000000 };
    var j: i32 = 0;
    while (j < 3) {
        w = Swap { a: w.b, b: w.a };
        j = j + 1;
    }
    if (w.a / 1000000000 != 7) { return 93; }
    if (w.b / 1000000000 != 6) { return 94; }
    return __rc_underflow_count();
}
struct Swap { a: i64, b: i64 }`

func TestX86_64WideFieldReuse(t *testing.T) {
	if out, code := compileAndRunX86_64FreeOn(t, wideReuseSrc); code != 0 {
		t.Errorf("wide-field reuse churn: got %d, want 0 (90/91/92 = value corrupted, else underflow)\n%s", code, out)
	}
}

func TestArm64WideFieldReuse(t *testing.T) {
	if out, code := compileAndRunArm64FreeOn(t, wideReuseSrc); code != 0 {
		t.Errorf("wide-field reuse churn: got %d, want 0 (90/91/92 = value corrupted, else underflow)\n%s", code, out)
	}
}

func TestWASMWideFieldReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, wideReuseSrc); got != 0 {
		t.Errorf("wide-field reuse churn: got %d, want 0", got)
	}
	// Bounded high-water: the self-overwrite reuses the box in place, so the
	// churn allocates a constant number of boxes regardless of N.
	bump := func(n string) string {
		return `struct Acc { total: i64, scale: f64, n: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var p: Acc = Acc { total: 0, scale: 1.0, n: 0 };
    var i: i32 = 0;
    while (i < ` + n + `) {
        p = Acc { total: p.total + 1, scale: p.scale, n: p.n + 1 };
        i = i + 1;
    }
    if (p.n < 1) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bump("2000"))
	large := runWasm(t, bump("20000"))
	if small != large {
		t.Errorf("wide-field self-overwrite should be in-place (bounded): N=2000 -> %d, N=20000 -> %d", small, large)
	}
}
