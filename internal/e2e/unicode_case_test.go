package e2e

import "testing"

// Differential coverage for std/unicode across backends: simple case
// mapping (ASCII, Latin-1, Greek, Cyrillic, the code-point helpers, the
// non-letter passthrough, the ß caveat) AND the character-class
// predicates (CJK letter, Arabic-Indic digit, NBSP whitespace, Greek
// upper/lower).
//
// The tables are long STRING literals decoded in place (#5627), so this
// also exercises big-string-literal codegen and the two decode paths —
// constant-delta runs and alternating pair runs — plus the ASCII fast
// path that fronts every entry point. Each leg skips itself when its
// toolchain is absent.
const unicodeCaseProg = `
import "std/unicode" as unicode;
function main(): i32 {
    if (unicode.to_upper("hello") != "HELLO") { return 1; }
    if (unicode.to_lower("HeLLo") != "hello") { return 2; }
    if (unicode.to_upper("café") != "CAFÉ") { return 3; }
    if (unicode.to_lower("ÜBER") != "über") { return 4; }
    if (unicode.to_upper("αβγ") != "ΑΒΓ") { return 5; }
    if (unicode.to_upper("привет") != "ПРИВЕТ") { return 6; }
    if (!unicode.eq_ignore_case("Café", "cafÉ")) { return 7; }
    if (unicode.eq_ignore_case("abc", "abd")) { return 8; }
    if (unicode.to_upper_char(97) != 65) { return 9; }
    if (unicode.to_lower_char(913) != 945) { return 10; }
    if (unicode.to_upper("123!") != "123!") { return 11; }
    if (unicode.to_upper("straße") != "STRAßE") { return 12; }
    // character classes: CJK letter, Arabic-Indic digit, NBSP space
    if (!unicode.is_letter(0x4E2D) || unicode.is_letter(48)) { return 13; }
    if (!unicode.is_digit(0x0669) || !unicode.is_alnum(97)) { return 14; }
    if (!unicode.is_whitespace(0xA0) || unicode.is_whitespace(97)) { return 15; }
    if (!unicode.is_upper(0x391) || !unicode.is_lower(0x3B1)) { return 16; }
    // ASCII fast path vs decode path: the same prefix, both ways.
    if (unicode.to_upper("abc 1!") != "ABC 1!") { return 17; }
    if (unicode.to_upper("abc 1!é") != "ABC 1!É") { return 18; }
    // Alternating pair run (Latin Extended-A) — the kind-1 decode.
    if (unicode.to_lower_char(0x100) != 0x101) { return 19; }
    if (unicode.to_upper_char(0x101) != 0x100) { return 20; }
    if (unicode.to_upper("ăćĕ") != "ĂĆĔ") { return 21; }
    // Large constant delta, and a non-BMP mapping (4-byte UTF-8).
    if (unicode.to_upper_char(0x250) != 0x2C6F) { return 22; }
    if (unicode.to_upper_char(0x10428) != 0x10400) { return 23; }
    if (unicode.to_upper("𐐨") != "𐐀") { return 24; }
    // eq_ignore_case streams both operands: KELVIN SIGN (3 bytes) folds
    // to 'k' (1 byte), so equal text with unequal byte lengths.
    if (!unicode.eq_ignore_case("K", "k")) { return 25; }
    if (unicode.eq_ignore_case("abc", "ab")) { return 26; }
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
