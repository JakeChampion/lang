package e2e

// Differential coverage for std/num `cumsum` / `cumproduct` — the running
// (prefix) sums and products, the cumulative siblings of `sum` / `product`.
// `out[i]` reduces `xs[0..=i]`, so the last element equals the full `sum` /
// `product`. Checks the running values, the empty and single-element inputs,
// and both i32 and i64 element widths (the `Add + Zero` / `Mul + One` bounds
// are generic). Returns 42 iff every check holds across interp / x86-64 /
// wasm / arm64.

import "testing"

const numCumulativeProg = `
import "std/num";
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4];
    var cs: i32[] = num.cumsum(xs);
    if (cs.len() != 4 || cs[0] != 1 || cs[1] != 3 || cs[2] != 6 || cs[3] != 10) { return 1; }
    var cp: i32[] = num.cumproduct(xs);
    if (cp.len() != 4 || cp[0] != 1 || cp[1] != 2 || cp[2] != 6 || cp[3] != 24) { return 2; }
    // Empty and single-element.
    var e: i32[] = [];
    if (num.cumsum(e).len() != 0 || num.cumproduct(e).len() != 0) { return 3; }
    var one: i32[] = [42];
    if (num.cumsum(one)[0] != 42 || num.cumproduct(one)[0] != 42) { return 4; }
    // Last element equals the full reduction.
    if (num.cumsum(xs)[3] != num.sum(xs)) { return 5; }
    if (num.cumproduct(xs)[3] != num.product(xs)) { return 6; }
    // i64 width.
    var ls: i64[] = [1000000000, 2000000000, 3000000000];
    var lcs: i64[] = num.cumsum(ls);
    if (lcs[2] != 6000000000) { return 7; }
    return 42;
}
`

func TestNumCumulativeInterp(t *testing.T) {
	if got := runInterpExit(t, numCumulativeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestNumCumulativeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, numCumulativeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestNumCumulativeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, numCumulativeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestNumCumulativeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, numCumulativeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
