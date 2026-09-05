package e2e

import "testing"

// Differential coverage for std/i32.next_power_of_2 and std/i32.is_prime at the
// top of the signed 32-bit range, where both used to compute their loop
// condition in i32 and wrap (#8467). 2^30 is the largest power of two a signed
// i32 holds, so next_power_of_2 returns the 0 sentinel above it — the same
// convention as the i64 / u32 / u64 twins — instead of doubling past the sign
// boundary into an unbreakable loop. is_prime bounds its trial divisor by
// `i <= n / i`, which cannot overflow, so M31 (2147483647) reports prime while
// composites whose smallest factor sits at the root (46337 * 46327, 46327^2)
// still report composite. A regression here HANGS rather than failing, so these
// legs rely on the package test timeout.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64; each
// leg skips itself when its toolchain is absent.
const i32RootsPrimeProg = `
import "std/i32" as i32m;
function main(): i32 {
    if ((0).next_power_of_2() != 1) { return 1; }
    if ((5).next_power_of_2() != 8) { return 2; }
    if ((16).next_power_of_2() != 16) { return 3; }
    if ((1073741823).next_power_of_2() != 1073741824) { return 4; }   // 2^30 - 1
    if ((1073741824).next_power_of_2() != 1073741824) { return 5; }   // 2^30
    if ((1073741825).next_power_of_2() != 0) { return 6; }            // > 2^30 -> 0
    if ((2147483647).next_power_of_2() != 0) { return 7; }            // i32::MAX -> 0

    if (!(2).is_prime()) { return 10; }
    if (!(3).is_prime()) { return 11; }
    if ((1).is_prime()) { return 12; }
    if ((0 - 7).is_prime()) { return 13; }
    if (!(7919).is_prime()) { return 14; }
    if ((7917).is_prime()) { return 15; }                             // 3 * 7 * 13 * 29
    if (!(46337).is_prime()) { return 16; }                           // largest prime below 46340
    if ((2146654199).is_prime()) { return 17; }                       // 46337 * 46327
    if ((2146190929).is_prime()) { return 18; }                       // 46327^2
    if (!(2147483647).is_prime()) { return 19; }                      // M31, prime
    if (!(2147483629).is_prime()) { return 20; }
    if (!(2147483587).is_prime()) { return 21; }
    if ((2147483645).is_prime()) { return 22; }                       // 5 * 19 * 22605091
    if ((2147483646).is_prime()) { return 23; }                       // even
    return 42;
}
`

func TestI32RootsPrimeInterp(t *testing.T) {
	if got := runInterpExit(t, i32RootsPrimeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI32RootsPrimeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i32RootsPrimeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI32RootsPrimeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i32RootsPrimeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI32RootsPrimeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i32RootsPrimeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
