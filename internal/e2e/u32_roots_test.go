package e2e

import "testing"

// Differential coverage for std/u32.sqrt_floor / is_power_of_2 /
// next_power_of_2 / log2_floor — unsigned root/power helpers, the u32 mirror of
// the u64 set. sqrt_floor is exact up to (2^16-1)^2, is_power_of_2 /
// next_power_of_2 reach 2^31 (the largest power a u32 holds; next_power_of_2
// returns 0 above it), and log2_floor spans 0..31. None of them depend on
// 32-bit wrap-around arithmetic. Returns 42 iff every check holds across interp
// / x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
const u32RootsProg = `
import "std/u32" as u32m;
function main(): i32 {
    if ((100 as u32).sqrt_floor() != (10 as u32)) { return 1; }
    if ((99 as u32).sqrt_floor() != (9 as u32)) { return 2; }
    if ((4294836225 as u32).sqrt_floor() != (65535 as u32)) { return 3; }  // (2^16-1)^2
    if ((0 as u32).sqrt_floor() != (0 as u32)) { return 4; }
    if ((1 as u32).sqrt_floor() != (1 as u32)) { return 5; }
    if (!(2147483648 as u32).is_power_of_2()) { return 6; }               // 2^31
    if (!(1 as u32).is_power_of_2()) { return 7; }
    if ((6 as u32).is_power_of_2()) { return 8; }
    if ((0 as u32).is_power_of_2()) { return 9; }
    if ((5 as u32).next_power_of_2() != (8 as u32)) { return 10; }
    if ((16 as u32).next_power_of_2() != (16 as u32)) { return 11; }
    if ((2147483648 as u32).next_power_of_2() != (2147483648 as u32)) { return 12; }  // 2^31
    if (((2147483648 as u32) + (1 as u32)).next_power_of_2() != (0 as u32)) { return 13; }  // > 2^31 -> 0
    if ((2147483648 as u32).log2_floor() != 31) { return 14; }
    if ((1 as u32).log2_floor() != 0) { return 15; }
    if ((0 as u32).log2_floor() != (0 - 1)) { return 16; }               // sentinel
    return 42;
}
`

func TestU32RootsInterp(t *testing.T) {
	if got := runInterpExit(t, u32RootsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestU32RootsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, u32RootsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestU32RootsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, u32RootsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestU32RootsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, u32RootsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
