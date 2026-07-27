package e2e

import "testing"

// normalizeProgram pins std/unicode's canonical normalization (#5631,
// decision D5) across every backend.
//
// The bug this closes: "é" written as one code point (U+00E9) and as
// "e" plus a combining acute are the same text, and before this they
// compared unequal with nothing in the language able to say otherwise.
//
// `==` deliberately STAYS byte equality — step 1 asserts that, because
// it is a design decision, not an accident. Making `==` canonical would
// put a normalization pass in front of every string-keyed map lookup;
// Rust, Go, Python, JS, C# and Java all declined for that reason, and
// Fern's handler path is full of such maps. Canonical comparison is the
// explicit `eq_canonical`.
//
// Exits 0 on success, a distinct code per failed step.
const normalizeProgram = `
import "std/unicode" as unicode;
import "std/utf8" as utf8;

function cp(n: i32): string { return utf8.utf8_encode((n) as char); }

function main(): i32 {
    var nfc_e: string = cp(233);        // U+00E9, precomposed
    var nfd_e: string = "e" + cp(769);  // e + COMBINING ACUTE

    // == is byte equality, by design. If this ever starts passing,
    // someone has made == normalize and the map-lookup cost came with it.
    if (nfc_e == nfd_e) { return 1; }

    // ... and eq_canonical is the way to ask the other question.
    if (!unicode.eq_canonical(nfc_e, nfd_e)) { return 2; }
    if (!unicode.eq_canonical("café", "cafe" + cp(769))) { return 3; }
    if (unicode.eq_canonical("café", "cafe")) { return 4; }

    // Both directions of the conversion.
    if (unicode.nfc(nfd_e) != nfc_e) { return 5; }
    if (unicode.nfd(nfc_e) != nfd_e) { return 6; }

    // Round-trip property: NFC(NFD(x)) == NFC(x) for every form of x.
    if (unicode.nfc(unicode.nfd(nfc_e)) != unicode.nfc(nfc_e)) { return 7; }
    if (unicode.nfc(unicode.nfd(nfd_e)) != unicode.nfc(nfc_e)) { return 8; }
    // Idempotence.
    if (unicode.nfd(unicode.nfd(nfc_e)) != unicode.nfd(nfc_e)) { return 9; }

    // Hangul is ALGORITHMIC, not table-driven -- a classic source of
    // normalization bugs, so pin both directions including the trailing
    // jamo (T) case.
    if (unicode.nfd(cp(44032)) != cp(4352) + cp(4449)) { return 10; }
    if (unicode.nfc(cp(4352) + cp(4449)) != cp(44032)) { return 11; }
    if (unicode.nfd(cp(44033)) != cp(4352) + cp(4449) + cp(4520)) { return 12; }
    if (unicode.nfc(cp(4352) + cp(4449) + cp(4520)) != cp(44033)) { return 13; }

    // Combining marks reorder by combining class: cedilla (202) sorts
    // ahead of acute (230) regardless of the order written.
    if (unicode.nfd("a" + cp(769) + cp(807)) != "a" + cp(807) + cp(769)) { return 14; }
    // Reordering is STABLE: equal classes keep their written order.
    if (unicode.nfd("a" + cp(783) + cp(769)) != "a" + cp(783) + cp(769)) { return 15; }

    // Singleton decompositions never recompose. ANGSTROM SIGN normalizes
    // to A-with-ring under NFC, NOT back to itself.
    if (unicode.nfc(cp(8491)) != cp(197)) { return 16; }
    if (unicode.nfd(cp(8491)) != "A" + cp(778)) { return 17; }
    if (unicode.nfc(cp(8486)) != cp(937)) { return 18; }   // OHM -> Greek omega

    // Composition exclusion: DEVANAGARI KA WITH NUKTA decomposes and is
    // NOT put back together, even though the pair has a composite.
    if (unicode.nfc(cp(2392)) != cp(2325) + cp(2364)) { return 19; }
    if (unicode.nfc(cp(2325) + cp(2364)) != cp(2325) + cp(2364)) { return 20; }

    // Multi-step decomposition: U+1E69 expands to three code points.
    if (unicode.nfd(cp(7785)) != "s" + cp(803) + cp(775)) { return 21; }
    if (unicode.nfc("s" + cp(803) + cp(775)) != cp(7785)) { return 22; }

    // Quick checks agree with the conversions.
    if (!unicode.is_nfc(nfc_e) || unicode.is_nfc(nfd_e)) { return 23; }
    if (!unicode.is_nfd(nfd_e) || unicode.is_nfd(nfc_e)) { return 24; }

    // A Maybe that cannot be decided locally: the pair U+1E63 + cedilla
    // does not compose, yet the string is not NFC, because U+1E63 splits
    // and the cedilla reorders in front of the dot-below.
    if (unicode.is_nfc(cp(7779) + cp(807))) { return 25; }

    // ASCII is trivially both forms and must pass through untouched.
    if (unicode.nfc("hello") != "hello") { return 26; }
    if (unicode.nfd("hello") != "hello") { return 27; }
    if (!unicode.is_nfc("hello") || !unicode.is_nfd("hello")) { return 28; }
    if (unicode.nfc("") != "") { return 29; }
    if (!unicode.is_nfc("")) { return 30; }

    // eq_canonical is reflexive on the byte-equal fast path and does not
    // claim unrelated strings are equal.
    if (!unicode.eq_canonical("abc", "abc")) { return 31; }
    if (unicode.eq_canonical("abc", "abd")) { return 32; }
    return 0;
}
`

func TestStringNormalizeInterp(t *testing.T) {
	if got := runInterpExit(t, normalizeProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestStringNormalizeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, normalizeProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestStringNormalizeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, normalizeProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestStringNormalizeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, normalizeProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
