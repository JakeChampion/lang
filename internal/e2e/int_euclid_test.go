package e2e

import "testing"

// Differential coverage for the integer div_euclid / rem_euclid helpers —
// std/i32, std/i64, std/u32, std/u64. Euclidean division yields a remainder
// always in [0, |rhs|) (unlike / and %, which truncate toward zero and leave a
// sign-of-dividend remainder), so it's the "true modulo" for wrap-around
// indexing where n % rhs would go negative. The signed versions round the
// truncating quotient one step further from zero when the truncating remainder
// is negative; the unsigned versions coincide with / and %. Checks all four
// sign quadrants, the wrap-around case ((-1).rem_euclid(3) == 2), and the
// div/rem identity n == rhs*div_euclid(rhs) + rem_euclid(rhs). Returns 42 iff
// every check holds across interp / x86-64 / wasm / arm64; each leg skips
// itself when its toolchain is absent.
const intEuclidProg = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    // i32: the four (sign of n, sign of rhs) quadrants.
    if ((7).div_euclid(3) != 2 || (7).rem_euclid(3) != 1) { return 1; }
    if ((0 - 7).div_euclid(3) != (0 - 3) || (0 - 7).rem_euclid(3) != 2) { return 2; }
    if ((7).div_euclid(0 - 3) != (0 - 2) || (7).rem_euclid(0 - 3) != 1) { return 3; }
    if ((0 - 7).div_euclid(0 - 3) != 3 || (0 - 7).rem_euclid(0 - 3) != 2) { return 4; }
    if ((0 - 1).rem_euclid(3) != 2) { return 5; }                    // wrap-around
    if ((6).div_euclid(3) != 2 || (6).rem_euclid(3) != 0) { return 6; }  // exact
    var a: i32 = 0 - 17; var b: i32 = 5;
    if (b * a.div_euclid(b) + a.rem_euclid(b) != a) { return 7; }    // div/rem identity
    // i64: negative dividend, and a value past the i32 range.
    if ((0 - 7 as i64).div_euclid(3 as i64) != (0 - 3 as i64)) { return 10; }
    if ((0 - 7 as i64).rem_euclid(3 as i64) != (2 as i64)) { return 11; }
    if ((0 - 1000000000001 as i64).rem_euclid(7 as i64) != (5 as i64)) { return 12; }
    var la: i64 = 0 - 17; var lb: i64 = 5;
    if (lb * la.div_euclid(lb) + la.rem_euclid(lb) != la) { return 13; }
    // u32 / u64: coincide with / and %.
    if ((17 as u32).div_euclid(5 as u32) != (3 as u32) || (17 as u32).rem_euclid(5 as u32) != (2 as u32)) { return 20; }
    if ((17 as u64).div_euclid(5 as u64) != (3 as u64) || (17 as u64).rem_euclid(5 as u64) != (2 as u64)) { return 21; }
    return 42;
}
`

func TestIntEuclidInterp(t *testing.T) {
	if got := runInterpExit(t, intEuclidProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntEuclidX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intEuclidProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntEuclidWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intEuclidProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntEuclidArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intEuclidProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
