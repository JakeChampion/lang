package e2e

import "testing"

// stringUnicodeCaseProg pins the #5630 flip: on `string`, the unqualified
// `to_upper` / `to_lower` are Unicode-correct, and the byte fold that used
// to own those names is now spelled `to_ascii_upper` / `to_ascii_lower`.
//
// The contrast pairs are the point of the test. `"café".to_upper()` and
// `"café".to_ascii_upper()` differ in exactly one code point, and before
// this change there was no way to ask for either one by name — the ASCII
// answer was all you could get, whatever you meant. Each numbered exit
// isolates one claim so a backend divergence names itself.
//
// Mapping is SIMPLE (1:1), so `ß` survives `to_upper` unchanged rather
// than expanding to `SS`; that is asserted here (step 20) so the follow-up
// that makes mapping full has a test to flip rather than a gap to notice.
const stringUnicodeCaseProg = `
import "std/string";

function main(): i32 {
    // Unicode: the accent, the Greek, the Cyrillic all case-map.
    if ("café".to_upper() != "CAFÉ") { return 1; }
    if ("CAFÉ".to_lower() != "café") { return 2; }
    if ("über".to_upper() != "ÜBER") { return 3; }
    if ("αβγ".to_upper() != "ΑΒΓ") { return 4; }
    if ("ΑΒΓ".to_lower() != "αβγ") { return 5; }
    if ("привет".to_upper() != "ПРИВЕТ") { return 6; }
    if ("ПРИВЕТ".to_lower() != "привет") { return 7; }

    // ASCII fast path through the SAME entry point: pure-ASCII input
    // never reaches a table, and must agree byte-for-byte.
    if ("hello".to_upper() != "HELLO") { return 8; }
    if ("HeLLo".to_lower() != "hello") { return 9; }
    if ("abc 123!".to_upper() != "ABC 123!") { return 10; }
    if ("".to_upper() != "") { return 11; }
    if ("".to_lower() != "") { return 12; }

    // to_ascii_* is the old byte fold, unchanged: non-ASCII passes through.
    if ("café".to_ascii_upper() != "CAFé") { return 13; }
    if ("CAFÉ".to_ascii_lower() != "cafÉ") { return 14; }
    if ("hello".to_ascii_upper() != "HELLO") { return 15; }
    if ("HeLLo".to_ascii_lower() != "hello") { return 16; }
    if ("".to_ascii_upper() != "") { return 17; }

    // The two families agree on ASCII and disagree off it — the whole
    // reason both names exist.
    if ("mixed 9!".to_upper() != "mixed 9!".to_ascii_upper()) { return 18; }
    if ("café".to_upper() == "café".to_ascii_upper()) { return 19; }

    // Simple (1:1) mapping: no 1→N expansion yet.
    if ("straße".to_upper() != "STRAßE") { return 20; }

    // Composition: chained, and on a non-literal receiver.
    var s: string = "  Hello Wörld  ";
    if (s.trim().to_upper() != "HELLO WÖRLD") { return 21; }
    if ("AbC".to_lower().to_upper() != "ABC") { return 22; }

    // Locale-independent: no Turkish dotless-i tailoring in the default path.
    if ("I".to_lower() != "i") { return 23; }
    return 0;
}
`

func TestStringUnicodeCaseInterp(t *testing.T) {
	if got := runInterpExit(t, stringUnicodeCaseProg); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestStringUnicodeCaseX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringUnicodeCaseProg); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestStringUnicodeCaseWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringUnicodeCaseProg); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestStringUnicodeCaseArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringUnicodeCaseProg); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
