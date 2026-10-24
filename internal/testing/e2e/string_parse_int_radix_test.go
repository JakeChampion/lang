package e2e

import "testing"

const stringParseIntRadixProg = `
import "std/string";
function val(o: Option[i32], d: i32): i32 { match (o) { Some(v) => { return v; }, None => { return d; } } }
function main(): i32 {
    if (val("ff".parse_int_radix(16), 0 - 1) != 255) { return 1; }
    if (val("101".parse_int_radix(2), 0 - 1) != 5) { return 2; }
    if (val("777".parse_int_radix(8), 0 - 1) != 511) { return 3; }   // octal
    if (val("z".parse_int_radix(36), 0 - 1) != 35) { return 4; }     // base 36
    if (val("-1a".parse_int_radix(16), 0) != 0 - 26) { return 5; }   // negative
    if (val("+10".parse_int_radix(10), 0 - 1) != 10) { return 6; }   // plus sign
    if (val("FF".parse_int_radix(16), 0 - 1) != 255) { return 7; }   // uppercase digits
    // failure modes -> None -> the -99 fallback
    if (val("2".parse_int_radix(2), 0 - 99) != 0 - 99) { return 8; } // digit >= base
    if (val("".parse_int_radix(10), 0 - 99) != 0 - 99) { return 9; } // empty
    if (val("5".parse_int_radix(1), 0 - 99) != 0 - 99) { return 10; }// base too small
    if (val("5".parse_int_radix(37), 0 - 99) != 0 - 99) { return 11; }// base too large
    if (val("-".parse_int_radix(10), 0 - 99) != 0 - 99) { return 12; }// lone sign
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
