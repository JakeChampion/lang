package e2e

// Differential coverage for std/array `running_max` / `running_min` — the
// running (prefix) maximum / minimum, the `cmp.Ord` cumulative siblings of
// `num.cumsum`. `out[i]` reduces `xs[0..=i]`, so the result is monotonic and
// its last element is the overall extremum. Checks the running values, the
// monotonicity + last-element-is-overall properties, and the empty / single
// inputs. Returns 42 iff every check holds across interp / x86-64 / wasm /
// arm64.

import "testing"

const arrayRunningMinMaxProg = `
import "std/array";
function main(): i32 {
    var xs: i32[] = [3, 1, 4, 1, 5, 9, 2, 6];
    var rmax: i32[] = array.running_max(xs);   // 3,3,4,4,5,9,9,9
    if (rmax.len() != 8) { return 1; }
    if (rmax[0] != 3 || rmax[2] != 4 || rmax[4] != 5 || rmax[7] != 9) { return 2; }
    var rmin: i32[] = array.running_min(xs);   // 3,1,1,1,1,1,1,1
    if (rmin[0] != 3 || rmin[1] != 1 || rmin[7] != 1) { return 3; }
    // Monotonicity: running_max never decreases, running_min never increases.
    var i: i32 = 1;
    while (i < 8) {
        if (rmax[i] < rmax[i - 1]) { return 4; }
        if (rmin[i] > rmin[i - 1]) { return 5; }
        i = i + 1;
    }
    // Empty and single-element.
    var e: i32[] = [];
    if (array.running_max(e).len() != 0 || array.running_min(e).len() != 0) { return 6; }
    var one: i32[] = [42];
    if (array.running_max(one)[0] != 42 || array.running_min(one)[0] != 42) { return 7; }
    return 42;
}
`

func TestArrayRunningMinMaxInterp(t *testing.T) {
	if got := runInterpExit(t, arrayRunningMinMaxProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayRunningMinMaxX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayRunningMinMaxProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayRunningMinMaxWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayRunningMinMaxProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayRunningMinMaxArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayRunningMinMaxProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
