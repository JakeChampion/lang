package e2e

import "testing"

// Differential coverage for std/string.zfill(width) — left-pad a numeric string
// with '0' to at least width bytes, keeping a leading sign ('+' / '-') in FRONT
// of the zeros (Python's str.zfill), unlike pad_start which would zero-pad
// before the sign. Already-width-or-longer strings pass through; the sign counts
// toward width. Returns 42 iff every check holds across interp / x86-64 / wasm /
// arm64; each leg skips itself when its toolchain is absent.
const stringZfillProg = `
import "std/string";
function main(): i32 {
    if ("42".zfill(5) != "00042") { return 1; }
    if ("-42".zfill(5) != "-0042") { return 2; }    // sign stays in front
    if ("+42".zfill(5) != "+0042") { return 3; }
    if ("5".zfill(1) != "5") { return 4; }           // already >= width
    if ("123".zfill(3) != "123") { return 5; }       // exactly width
    if ("".zfill(3) != "000") { return 6; }          // empty -> all zeros
    if ("-".zfill(4) != "-000") { return 7; }        // lone sign
    if ("7".zfill(0) != "7") { return 8; }           // width 0 -> unchanged
    if ("abc".zfill(5) != "00abc") { return 9; }     // non-numeric: no sign, plain pad
    if ("-7".zfill(2) != "-7") { return 10; }        // already width incl. sign
    return 42;
}
`

func TestStringZfillInterp(t *testing.T) {
	if got := runInterpExit(t, stringZfillProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringZfillX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringZfillProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringZfillWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringZfillProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringZfillArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringZfillProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
