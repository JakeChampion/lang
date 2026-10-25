package e2e

import "testing"

const unicodeCaseProg = `
import "std/unicode" as unicode;
function main(): i32 {
    if (("hello").to_upper() != "HELLO") { return 1; }
    if (("HeLLo").to_lower() != "hello") { return 2; }
    if (("café").to_upper() != "CAFÉ") { return 3; }
    if (("ÜBER").to_lower() != "über") { return 4; }
    if (("αβγ").to_upper() != "ΑΒΓ") { return 5; }
    if (("привет").to_upper() != "ПРИВЕТ") { return 6; }
    if (!("Café").eq_ignore_case("cafÉ")) { return 7; }
    if (("abc").eq_ignore_case("abd")) { return 8; }
    if (((97 as char).to_upper() as i32) != 65) { return 9; }
    if (((913 as char).to_lower() as i32) != 945) { return 10; }
    if (("123!").to_upper() != "123!") { return 11; }
    if (("straße").to_upper() != "STRASSE") { return 12; }
    // character classes: CJK letter, Arabic-Indic digit, NBSP space
    if (!((0x4E2D as char).is_letter()) || ((48 as char).is_letter())) { return 13; }
    if (!((0x0669 as char).is_digit()) || !((97 as char).is_alphanumeric())) { return 14; }
    if (!((0xA0 as char).is_whitespace()) || ((97 as char).is_whitespace())) { return 15; }
    if (!((0x391 as char).is_upper()) || !((0x3B1 as char).is_lower())) { return 16; }
    // ASCII fast path vs decode path: the same prefix, both ways.
    if (("abc 1!").to_upper() != "ABC 1!") { return 17; }
    if (("abc 1!é").to_upper() != "ABC 1!É") { return 18; }
    // Alternating pair run (Latin Extended-A) — the kind-1 decode.
    if (((0x100 as char).to_lower() as i32) != 0x101) { return 19; }
    if (((0x101 as char).to_upper() as i32) != 0x100) { return 20; }
    if (("ăćĕ").to_upper() != "ĂĆĔ") { return 21; }
    // Large constant delta, and a non-BMP mapping (4-byte UTF-8).
    if (((0x250 as char).to_upper() as i32) != 0x2C6F) { return 22; }
    if (((0x10428 as char).to_upper() as i32) != 0x10400) { return 23; }
    if (("𐐨").to_upper() != "𐐀") { return 24; }
    // eq_ignore_case streams both operands: KELVIN SIGN (3 bytes) folds
    // to 'k' (1 byte), so equal text with unequal byte lengths.
    if (!("K").eq_ignore_case("k")) { return 25; }
    if (("abc").eq_ignore_case("ab")) { return 26; }
    return 42;
}
`

func TestUnicodeCaseInterp(t *testing.T) {
	if got := runInterpExit(t, unicodeCaseProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestUnicodeCaseX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, unicodeCaseProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestUnicodeCaseWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, unicodeCaseProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestUnicodeCaseArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, unicodeCaseProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
