package e2e

import "testing"

// Differential coverage for std/i64.to_string_radix(base) — render an i64 in an
// arbitrary base (2..36, digits 0-9a-z, signed), the wider counterpart to
// std/i32's. The magnitude is a u64 (unsigned negation), so it exercises
// unsigned u64 div/rem AND renders i64::MIN cleanly (2^63 -> "-8000000000000000"
// in hex). Values past the i32 range (2^32, i64::MAX/MIN) exercise the full
// width. Returns 42 iff every check holds across interp / x86-64 / wasm / arm64;
// each leg skips itself when its toolchain is absent.
const i64ToStringRadixProg = `
import "std/i64";
function main(): i32 {
    if ((255 as i64).to_string_radix(16) != "ff") { return 1; }
    if ((5 as i64).to_string_radix(2) != "101") { return 2; }
    if ((0 as i64).to_string_radix(10) != "0") { return 3; }
    if (((0 as i64) - 26).to_string_radix(16) != "-1a") { return 4; }  // negative
    if ((35 as i64).to_string_radix(36) != "z") { return 5; }          // base 36
    if ((4294967296 as i64).to_string_radix(16) != "100000000") { return 6; } // 2^32
    if ((9223372036854775807 as i64).to_string_radix(16) != "7fffffffffffffff") { return 7; } // i64::MAX
    var mn: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    if (mn.to_string_radix(16) != "-8000000000000000") { return 8; }   // i64::MIN
    if ((5 as i64).to_string_radix(1) != "") { return 9; }             // base too small
    if ((5 as i64).to_string_radix(37) != "") { return 10; }           // base too large
    return 42;
}
`

func TestI64ToStringRadixInterp(t *testing.T) {
	if got := runInterpExit(t, i64ToStringRadixProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64ToStringRadixX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64ToStringRadixProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64ToStringRadixWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i64ToStringRadixProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI64ToStringRadixArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64ToStringRadixProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
