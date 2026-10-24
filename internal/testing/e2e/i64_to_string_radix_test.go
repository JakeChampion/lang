package e2e

import "testing"

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
    let mn: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
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
