package e2e

import "testing"

const stringParseIntRadixProg = `
import "std/i32";
import "std/result";
import "std/string";
function val(o: Option[i32], d: i32): i32 { match (o) { Some(v) => { return v; }, None => { return d; } } }
function main(): i32 {
    if (val(i32.parse_i32_radix("ff", 16).ok(), 0 - 1) != 255) { return 1; }
    if (val(i32.parse_i32_radix("101", 2).ok(), 0 - 1) != 5) { return 2; }
    if (val(i32.parse_i32_radix("777", 8).ok(), 0 - 1) != 511) { return 3; }   // octal
    if (val(i32.parse_i32_radix("z", 36).ok(), 0 - 1) != 35) { return 4; }     // base 36
    if (val(i32.parse_i32_radix("-1a", 16).ok(), 0) != 0 - 26) { return 5; }   // negative
    if (val(i32.parse_i32_radix("+10", 10).ok(), 0 - 1) != 10) { return 6; }   // plus sign
    if (val(i32.parse_i32_radix("FF", 16).ok(), 0 - 1) != 255) { return 7; }   // uppercase digits
    // failure modes -> None -> the -99 fallback
    if (val(i32.parse_i32_radix("2", 2).ok(), 0 - 99) != 0 - 99) { return 8; } // digit >= base
    if (val(i32.parse_i32_radix("", 10).ok(), 0 - 99) != 0 - 99) { return 9; } // empty
    if (val(i32.parse_i32_radix("5", 1).ok(), 0 - 99) != 0 - 99) { return 10; }// base too small
    if (val(i32.parse_i32_radix("5", 37).ok(), 0 - 99) != 0 - 99) { return 11; }// base too large
    if (val(i32.parse_i32_radix("-", 10).ok(), 0 - 99) != 0 - 99) { return 12; }// lone sign
    return 42;
}
`

func TestStringParseIntRadixInterp(t *testing.T) {
	if got := runInterpExit(t, stringParseIntRadixProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringParseIntRadixX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringParseIntRadixProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringParseIntRadixWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringParseIntRadixProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringParseIntRadixArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringParseIntRadixProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
