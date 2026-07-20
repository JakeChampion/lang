package e2e

import "testing"

// Differential coverage for std/i64.sqrt_floor / is_power_of_2 /
// is_perfect_square — the i64 siblings of the std/i32 predicates, exact into
// the i64 range where the i32 versions would overflow. sqrt_floor is Newton's
// method (floor of √n), is_power_of_2 is the n&(n-1) bit trick, and
// is_perfect_square is sqrt_floor(n)²==n. Returns 42 iff every check holds
// across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const i64RootsProg = `
import "std/i64" as i64m;
function main(): i32 {
    if ((100 as i64).sqrt_floor() != (10 as i64)) { return 1; }
    if ((99 as i64).sqrt_floor() != (9 as i64)) { return 2; }
    if ((10000000000 as i64).sqrt_floor() != (100000 as i64)) { return 3; }   // past i32 range
    if ((0 as i64).sqrt_floor() != (0 as i64)) { return 4; }
    if ((1 as i64).sqrt_floor() != (1 as i64)) { return 5; }
    if (!(4611686018427387904 as i64).is_power_of_2()) { return 6; }          // 2^62
    if (!(1 as i64).is_power_of_2()) { return 7; }
    if ((6 as i64).is_power_of_2()) { return 8; }
    if ((0 as i64).is_power_of_2()) { return 9; }
    if (((0 as i64) - 4).is_power_of_2()) { return 10; }                      // negatives false
    if (!(1000000 as i64).is_perfect_square()) { return 11; }
    if (!(1000000000000 as i64).is_perfect_square()) { return 12; }          // (10^6)^2, past i32
    if ((1000001 as i64).is_perfect_square()) { return 13; }
    if (((0 as i64) - 4).is_perfect_square()) { return 14; }
    return 42;
}
`

func TestI64RootsInterp(t *testing.T) {
	if got := runInterpExit(t, i64RootsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64RootsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64RootsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64RootsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i64RootsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI64RootsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64RootsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
