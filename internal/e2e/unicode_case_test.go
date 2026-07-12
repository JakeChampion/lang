package e2e

import "testing"

// Differential coverage for std/unicode's simple case mapping across
// backends. The program returns 42 iff every mapping holds — ASCII,
// Latin-1 (café/ÜBER), Greek, Cyrillic, the code-point helpers, the
// non-letter passthrough, and the ß simple-mapping caveat. The tables
// are large i32[] literals (~1450 entries each), so this also exercises
// big-array-literal codegen on every backend. Each leg skips itself
// when its toolchain is absent.
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
