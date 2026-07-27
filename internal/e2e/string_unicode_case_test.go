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
// Mapping is FULL: a code point may expand to several, so `ß` uppercases
// to `SS` and the result can be longer than the input in both bytes and
// code points. The Greek Final_Sigma context rule is the one piece still
// missing, and step 41 pins its current (wrong-per-Unicode) answer so the
// follow-up has a test to flip rather than a gap to notice.
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

    // FULL (1→N) mapping: one code point can become several.
    if ("straße".to_upper() != "STRASSE") { return 20; }

    // Composition: chained, and on a non-literal receiver.
    var s: string = "  Hello Wörld  ";
    if (s.trim().to_upper() != "HELLO WÖRLD") { return 21; }
    if ("AbC".to_lower().to_upper() != "ABC") { return 22; }

    // Locale-independent: no Turkish dotless-i tailoring in the default path.
    if ("I".to_lower() != "i") { return 23; }

    // capitalize / title_case / swap_case: same split, same contrast.
    if ("élan".capitalize() != "Élan") { return 24; }
    if ("élan".to_ascii_capitalize() != "élan") { return 25; }
    if ("hello".capitalize() != "Hello") { return 26; }
    if ("".capitalize() != "") { return 27; }

    if ("élan vital".title_case() != "Élan Vital") { return 28; }
    if ("the quick brown FOX".title_case() != "The Quick Brown FOX") { return 29; }
    if ("hello world".to_ascii_title_case() != "Hello World") { return 30; }
    if ("".title_case() != "") { return 31; }

    if ("Élan".swap_case() != "éLAN") { return 32; }
    // The byte fold swaps the ASCII tail but leaves É alone: ÉLAN, not éLAN.
    if ("Élan".to_ascii_swap_case() != "ÉLAN") { return 33; }
    if ("Hello World".swap_case() != "hELLO wORLD") { return 34; }
    if ("123!@#".swap_case() != "123!@#") { return 35; }
    // Still its own inverse, on ASCII and beyond it.
    if ("Hello, World!".swap_case().swap_case() != "Hello, World!") { return 36; }
    if ("Élan Vital".swap_case().swap_case() != "Élan Vital") { return 37; }

    // The Unicode title_case breaks on any whitespace; the byte one only
    // on U+0020. This is the one place the two differ on pure ASCII.
    if ("a\tb".title_case() != "A\tB") { return 38; }
    if ("a\tb".to_ascii_title_case() != "A\tb") { return 39; }

    // Full mapping across expansion lengths and scripts, and the length
    // growth it implies. The byte fold leaves every one of these alone.
    if ("ﬁsh".to_upper() != "FISH") { return 40; }
    if ("ﬄ".to_upper() != "FFL") { return 41; }
    if ("և".to_upper() != "ԵՒ") { return 42; }
    if ("İ".to_lower().len() != 3) { return 43; }
    if ("ß".to_ascii_upper() != "ß") { return 44; }
    // 1 code point becomes 2, but the BYTE length is unchanged: ß is two
    // bytes and SS is two bytes. Expansion in code points does not imply
    // expansion in bytes — İ (step 43) is the case where bytes do grow.
    if ("ß".to_upper() != "SS") { return 45; }
    // Not idempotent in the "maps to itself" sense — SS is already upper.
    if ("ß".to_upper().to_upper() != "SS") { return 46; }

    // Final_Sigma is NOT applied: a word-final Σ lowercases to σ, not ς.
    // Unicode says "σοφος"; flip this when the context rule lands.
    if ("ΣΟΦΟΣ".to_lower() != "σοφοσ") { return 47; }

    // Case FOLDING is a third operation, not lowercasing. ß folds to ss,
    // so these are equal under eq_ignore_case and not under to_lower.
    if (!"ß".eq_ignore_case("ss")) { return 48; }
    if (!"STRASSE".eq_ignore_case("straße")) { return 49; }
    if (!"ſ".eq_ignore_case("s")) { return 50; }
    if ("ß".case_fold() != "ss") { return 51; }
    if ("ß".to_lower() != "ß") { return 52; }
    // The ASCII-only comparator is unchanged and still says no.
    if ("ß".eq_ignore_ascii_case("ss")) { return 53; }
    // Both agree on a protocol token, which is the common case.
    if (!"Content-Type".eq_ignore_case("content-type")) { return 54; }
    if (!"Content-Type".eq_ignore_ascii_case("content-type")) { return 55; }
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
