package e2e

import "testing"

// graphemesProgram pins UAX #29 extended grapheme cluster segmentation
// (#5633, decision D6) across every backend.
//
// A "character" as a reader means it is a grapheme cluster, not a byte
// and not a code point. The cases below are the ones that break naive
// implementations, which is exactly why they are here rather than a
// handful of happy-path splits.
//
// `s.len()` stays BYTES and `s[i]` stays a byte index -- segmentation is
// opt-in, and these tests do not change that contract.
//
// Exits 0 on success, a distinct code per failed step.
const graphemesProgram = `
import "std/unicode" as unicode;
import "std/utf8" as utf8;

function cp(n: i32): string { return utf8.utf8_encode((n) as char); }

function main(): i32 {
    // A combining sequence is ONE cluster -- the headline case.
    if (unicode.grapheme_count("e" + cp(769)) != 1) { return 1; }
    if (unicode.graphemes("e" + cp(769)).len() != 1) { return 2; }

    // CRLF is a single cluster (GB3). Reversed, it is two.
    if (unicode.grapheme_count(cp(13) + cp(10)) != 1) { return 3; }
    if (unicode.grapheme_count(cp(10) + cp(13)) != 2) { return 4; }

    // Regional indicators pair into flags, and they pair in TWOS: four
    // indicators are two flags, not one long run (GB12/GB13).
    if (unicode.grapheme_count(cp(127468) + cp(127463)) != 1) { return 5; }
    if (unicode.grapheme_count(cp(127468) + cp(127463) + cp(127482) + cp(127480)) != 2) { return 6; }
    // An odd indicator trails as its own cluster.
    if (unicode.grapheme_count(cp(127468) + cp(127463) + cp(127482)) != 2) { return 7; }

    // Emoji ZWJ sequences are one cluster (GB11): a family is a family.
    var fam: string = cp(128104) + cp(8205) + cp(128105) + cp(8205) + cp(128103);
    if (unicode.grapheme_count(fam) != 1) { return 8; }
    // Profession emoji with a skin-tone modifier, likewise.
    var prof: string = cp(128104) + cp(127997) + cp(8205) + cp(128187);
    if (unicode.grapheme_count(prof) != 1) { return 9; }
    // But a ZWJ NOT preceded by a pictograph does not join.
    if (unicode.grapheme_count("a" + cp(8205) + cp(128105)) != 2) { return 10; }

    // Hangul jamo compose into one syllable cluster (GB6/7/8).
    if (unicode.grapheme_count(cp(4352) + cp(4449) + cp(4520)) != 1) { return 11; }
    if (unicode.grapheme_count(cp(4352) + cp(4449)) != 1) { return 12; }

    // Prepend attaches forwards (GB9b); SpacingMark attaches back (GB9a).
    if (unicode.grapheme_count(cp(1536) + "a") != 1) { return 13; }
    if (unicode.grapheme_count(cp(2325) + cp(2307)) != 1) { return 14; }

    // Plain ASCII and the empty string.
    if (unicode.grapheme_count("abc") != 3) { return 15; }
    if (unicode.grapheme_count("") != 0) { return 16; }
    if (unicode.graphemes("").len() != 0) { return 17; }

    // The clusters themselves are the right slices, not just the right
    // count: a combining pair is 3 bytes, the following ASCII 1.
    var gs: str[] = unicode.graphemes("e" + cp(769) + "x");
    if (gs.len() != 2) { return 18; }
    if (gs[0].len() != 3) { return 19; }
    if (gs[1].len() != 1) { return 20; }

    // reverse_graphemes keeps clusters intact -- the regression #5552
    // asked for. reverse_bytes would scramble this into invalid UTF-8.
    if (unicode.reverse_graphemes("e" + cp(769) + "x") != "x" + "e" + cp(769)) { return 21; }
    if (unicode.reverse_graphemes("abc") != "cba") { return 22; }
    if (unicode.reverse_graphemes("") != "") { return 23; }
    // A flag survives reversal as a unit.
    var flags: string = cp(127468) + cp(127463) + cp(127482) + cp(127480);
    if (unicode.reverse_graphemes(flags) != cp(127482) + cp(127480) + cp(127468) + cp(127463)) { return 24; }

    // len() is still BYTES, and is NOT the cluster count.
    if (("e" + cp(769)).len() != 3) { return 25; }
    return 0;
}
`

func TestGraphemesInterp(t *testing.T) {
	if got := runInterpExit(t, graphemesProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestGraphemesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, graphemesProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestGraphemesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, graphemesProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestGraphemesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, graphemesProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
