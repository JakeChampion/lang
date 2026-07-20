package e2e

import "testing"

// Differential coverage for std/i32.to_string_radix(base) — render an i32 in an
// arbitrary base (2..36, digits 0-9a-z, signed), the general form behind
// to_binary/to_oct/to_hex and the write-side inverse of
// string.parse_int_radix. Encodes results as string comparisons plus a
// parse_int_radix round-trip. Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64; each leg skips itself when its toolchain is absent.
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
    var s: string = (1234567).to_string_radix(36);
    match (s.parse_int_radix(36)) {
        Some(v) => { if (v != 1234567) { return 9; } },
        None => { return 10; }
    }
    // negative round-trip
    var t: string = (0 - 9999).to_string_radix(16);
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
