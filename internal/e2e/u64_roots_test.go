package e2e

import "testing"

// Differential coverage for std/u64.sqrt_floor / is_power_of_2 /
// next_power_of_2 / log2_floor — unsigned root/power helpers. Because they're
// unsigned they carry no negative-input cases and span the full u64 range:
// sqrt_floor is exact up to (2^32-1)^2, is_power_of_2 / next_power_of_2 reach
// 2^63 (the largest power a u64 holds; next_power_of_2 returns 0 above it), and
// log2_floor spans 0..63. Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
const u64RootsProg = `
import "std/u64" as u64m;
function main(): i32 {
    if ((100 as u64).sqrt_floor() != (10 as u64)) { return 1; }
    if ((99 as u64).sqrt_floor() != (9 as u64)) { return 2; }
    if ((18446744065119617025 as u64).sqrt_floor() != (4294967295 as u64)) { return 3; }  // (2^32-1)^2
    if ((0 as u64).sqrt_floor() != (0 as u64)) { return 4; }
    if ((1 as u64).sqrt_floor() != (1 as u64)) { return 5; }
    if (!(9223372036854775808 as u64).is_power_of_2()) { return 6; }        // 2^63
    if (!(1 as u64).is_power_of_2()) { return 7; }
    if ((6 as u64).is_power_of_2()) { return 8; }
    if ((0 as u64).is_power_of_2()) { return 9; }
    if ((5 as u64).next_power_of_2() != (8 as u64)) { return 10; }
    if ((16 as u64).next_power_of_2() != (16 as u64)) { return 11; }
    if ((9223372036854775808 as u64).next_power_of_2() != (9223372036854775808 as u64)) { return 12; }  // 2^63
    if (((9223372036854775808 as u64) + (1 as u64)).next_power_of_2() != (0 as u64)) { return 13; }     // > 2^63 -> 0
    if ((9223372036854775808 as u64).log2_floor() != 63) { return 14; }
    if ((1 as u64).log2_floor() != 0) { return 15; }
    if ((0 as u64).log2_floor() != (0 - 1)) { return 16; }                  // sentinel
    return 42;
}
`

func TestU64RootsInterp(t *testing.T) {
	if got := runInterpExit(t, u64RootsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestU64RootsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, u64RootsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestU64RootsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, u64RootsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestU64RootsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, u64RootsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
