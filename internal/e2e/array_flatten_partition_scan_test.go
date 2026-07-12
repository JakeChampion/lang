package e2e

import "testing"

// Differential coverage for the std/array combinators added alongside
// this test: flatten (T[][] → T[]), partition (→ (T[], T[]) — a
// tuple of arrays, exercising tuple + array-in-tuple codegen), and scan
// (running left fold). All are read-only over their inputs (build a
// fresh output), so they carry across every backend; each leg skips
// itself when its toolchain is absent.
const arrayComboProg = `
import "std/array" as array;
function is_even(x: i32): boolean { return x % 2 == 0; }
function add(a: i32, x: i32): i32 { return a + x; }
function main(): i32 {
    var f: i32[] = array.flatten([[1, 2], [], [3, 4]]);
    if (f.len() != 4 || f[0] != 1 || f[3] != 4) { return 1; }
    var p: (i32[], i32[]) = array.partition([1, 2, 3, 4, 5], is_even);
    if (p.0.len() != 2 || p.1.len() != 3) { return 2; }
    if (p.0[0] != 2 || p.0[1] != 4 || p.1[0] != 1 || p.1[2] != 5) { return 3; }
    var s: i32[] = array.scan([1, 2, 3, 4], 0, add);
    if (s.len() != 4 || s[0] != 1 || s[1] != 3 || s[2] != 6 || s[3] != 10) { return 4; }
    var pm: (i32[], i32[]) = [1, 2, 3, 4].partition(is_even);
    if (pm.0.len() != 2 || pm.1.len() != 2) { return 5; }
    var sm: i32[] = [1, 2, 3].scan(0, add);
    if (sm[2] != 6) { return 6; }
    return 42;
}
`

func TestArrayComboInterp(t *testing.T) {
	if got := runInterpExit(t, arrayComboProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayComboX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayComboProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayComboWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayComboProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayComboArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayComboProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
