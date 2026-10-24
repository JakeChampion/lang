package e2e

import "testing"

const i32ToStringRadixProg = `
import "std/i32";
import "std/string";
function main(): i32 {
    if ((255).to_string_radix(16) != "ff") { return 1; }
    if ((5).to_string_radix(2) != "101") { return 2; }
    if ((511).to_string_radix(8) != "777") { return 3; }   // octal
    if ((35).to_string_radix(36) != "z") { return 4; }     // base 36
    if ((0 - 26).to_string_radix(16) != "-1a") { return 5; } // negative
    if ((0).to_string_radix(10) != "0") { return 6; }
    if ((5).to_string_radix(1) != "") { return 7; }        // base too small -> ""
    if ((5).to_string_radix(37) != "") { return 8; }       // base too large -> ""
    // round-trips through parse_int_radix at the same base
    let s: string = (1234567).to_string_radix(36);
    match (s.parse_int_radix(36)) {
        Some(v) => { if (v != 1234567) { return 9; } },
        None => { return 10; }
    }
    // negative round-trip
    let t: string = (0 - 9999).to_string_radix(16);
    match (t.parse_int_radix(16)) {
        Some(v) => { if (v != 0 - 9999) { return 11; } },
        None => { return 12; }
    }
    return 42;
}
`

func TestI32ToStringRadixInterp(t *testing.T) {
	if got := runInterpExit(t, i32ToStringRadixProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI32ToStringRadixX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i32ToStringRadixProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI32ToStringRadixWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i32ToStringRadixProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI32ToStringRadixArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i32ToStringRadixProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
