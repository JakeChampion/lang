package e2e

import "testing"

// Differential coverage for std/i32.ordinal across backends: the st/nd/rd/th
// suffixes, the 11/12/13 exception (and its 111/112/113 recurrence), the
// tens carrying through (21st/102nd), zero, and a negative keeping its
// sign. Returns 42 iff every check holds. Each leg skips itself when its
// toolchain is absent.
const ordinalProg = `
import "std/i32";
function main(): i32 {
    if ((1).ordinal() != "1st") { return 1; }
    if ((2).ordinal() != "2nd") { return 2; }
    if ((3).ordinal() != "3rd") { return 3; }
    if ((4).ordinal() != "4th") { return 4; }
    if ((11).ordinal() != "11th") { return 5; }
    if ((12).ordinal() != "12th") { return 6; }
    if ((13).ordinal() != "13th") { return 7; }
    if ((21).ordinal() != "21st") { return 8; }
    if ((22).ordinal() != "22nd") { return 9; }
    if ((23).ordinal() != "23rd") { return 10; }
    if ((100).ordinal() != "100th") { return 11; }
    if ((101).ordinal() != "101st") { return 12; }
    if ((102).ordinal() != "102nd") { return 13; }
    if ((111).ordinal() != "111th") { return 14; }
    if ((112).ordinal() != "112th") { return 15; }
    if ((113).ordinal() != "113th") { return 16; }
    if ((0).ordinal() != "0th") { return 17; }
    if (((0) - 1).ordinal() != "-1st") { return 18; }
    return 42;
}
`

func TestOrdinalInterp(t *testing.T) {
	if got := runInterpExit(t, ordinalProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestOrdinalX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, ordinalProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestOrdinalWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, ordinalProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestOrdinalArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, ordinalProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
