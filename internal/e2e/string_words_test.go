package e2e

import "testing"

// wordsProgram pins UAX #29 word segmentation (#5552) across every
// backend.
//
// The expectations are not hand-reasoned: every count below was taken
// from `uniseg` (the same oracle cmd/unicodegen's table data comes
// from) before it was written down, and the state machine itself was
// checked against uniseg over every sequence of up to four code points
// drawn from all nineteen Word_Break classes plus Extended_Pictographic.
// What is here is the set of cases a naive `split(" ")` gets wrong.
//
// Two surfaces, and the difference between them is the point:
// `word_segments` is lossless -- the pieces concatenate back to the
// input, spaces and punctuation included -- while `words` keeps only
// the word-like ones.
//
// Exits 0 on success, a distinct code per failed step.
const wordsProgram = `
import "std/unicode" as unicode;
import "std/utf8" as utf8;

function cp(n: i32): string { return utf8.utf8_encode((n) as char); }

function main(): i32 {
    // A plain sentence: four words, and seven segments once the three
    // spaces are counted too.
    var s: string = "The quick brown fox";
    if (unicode.word_count(s) != 4) { return 1; }
    if (unicode.words(s).len() != 4) { return 2; }
    if (unicode.word_segments(s).len() != 7) { return 3; }
    if (unicode.words(s)[0] != "The") { return 4; }
    if (unicode.words(s)[3] != "fox") { return 5; }

    // The rules that make this more than split-on-space. An apostrophe
    // between letters joins both sides (WB6/WB7)...
    if (unicode.word_count("can't") != 1) { return 6; }
    if (unicode.word_segments("can't").len() != 1) { return 7; }
    // ...but a trailing one does not, since no letter follows it.
    if (unicode.word_segments("can'").len() != 2) { return 8; }

    // Numeric punctuation joins the same way (WB11/WB12).
    if (unicode.word_count("3.14") != 1) { return 9; }
    if (unicode.word_count("1,000") != 1) { return 10; }
    // A full stop with no digit after it is its own segment.
    if (unicode.word_segments("3.").len() != 2) { return 11; }

    // ExtendNumLet glues word-like things together (WB13a/WB13b).
    if (unicode.word_count("snake_case") != 1) { return 12; }
    // Letters bind to digits in both directions (WB9/WB10).
    if (unicode.word_count("x2") != 1) { return 13; }
    if (unicode.word_count("3rd") != 1) { return 14; }

    // No space needed to find a boundary -- this is what a naive
    // split(" ") gets wrong.
    if (unicode.word_count("hello,world") != 2) { return 15; }
    if (unicode.word_segments("hello,world").len() != 3) { return 16; }

    // A run of spaces is ONE segment, not one per space (WB3d).
    if (unicode.word_segments("a  b").len() != 3) { return 17; }
    if (unicode.word_count("a  b") != 2) { return 18; }

    // Non-ASCII is data, not a hazard: the whole point of #5552.
    if (unicode.word_count("café") != 1) { return 19; }
    if (unicode.word_segments("café").len() != 1) { return 20; }
    // Decomposed, too -- the combining acute does not split the word.
    if (unicode.word_count("caf" + "e" + cp(769)) != 1) { return 21; }
    if (unicode.word_segments("caf" + "e" + cp(769)).len() != 1) { return 22; }

    // Hebrew geresh (WB7a) and gershayim (WB7b/WB7c). The gershayim
    // joins only when a Hebrew letter follows it, so the pair alone is
    // two segments while the sandwich is one -- the same asymmetry the
    // apostrophe has above.
    if (unicode.word_segments(cp(1488) + cp(1523)).len() != 1) { return 23; }
    if (unicode.word_segments(cp(1488) + cp(1524)).len() != 2) { return 24; }
    if (unicode.word_segments(cp(1488) + cp(1524) + cp(1489)).len() != 1) { return 25; }

    // Katakana runs together; UAX #29 goes no further without a
    // dictionary, and this test does not pretend otherwise.
    if (unicode.word_segments(cp(12450) + cp(12452) + cp(12454)).len() != 1) { return 26; }

    // An emoji ZWJ family is ONE segment (WB3c) but is not word-LIKE,
    // so it survives word_segments and is dropped by words.
    var fam: string = cp(128104) + cp(8205) + cp(128105) + cp(8205) + cp(128103);
    if (unicode.word_segments(fam).len() != 1) { return 27; }
    if (unicode.word_count(fam) != 0) { return 28; }
    if (unicode.words(fam).len() != 0) { return 29; }

    // Regional indicators pair in TWOS (WB15/WB16): two flags, not one
    // run and not four indicators.
    var flags: string = cp(127468) + cp(127463) + cp(127482) + cp(127480);
    if (unicode.word_segments(flags).len() != 2) { return 30; }

    // CRLF stays one segment (WB3) and always breaks (WB3a/WB3b).
    if (unicode.word_segments("a" + cp(13) + cp(10) + "b").len() != 3) { return 31; }

    // Empty input.
    if (unicode.word_count("") != 0) { return 32; }
    if (unicode.words("").len() != 0) { return 33; }
    if (unicode.word_segments("").len() != 0) { return 34; }

    // The lossless property that separates the two surfaces:
    // word_segments concatenates back to the input exactly.
    var mixed: string = "Hello, world! 42 times.";
    if (unicode.word_segments(mixed).len() != 10) { return 35; }
    if (unicode.word_count(mixed) != 4) { return 36; }
    var segs: str[] = unicode.word_segments(mixed);
    var joined: string = "";
    var i: i32 = 0;
    while (i < segs.len()) {
        joined = joined + segs[i];
        i = i + 1;
    }
    if (joined != mixed) { return 37; }

    // words() agrees with word_count() on the same input, and the
    // segments it keeps are the word-like ones in order.
    if (unicode.words(mixed).len() != unicode.word_count(mixed)) { return 38; }
    if (unicode.words(mixed)[0] != "Hello") { return 39; }
    if (unicode.words(mixed)[2] != "42") { return 40; }
    if (unicode.words(mixed)[3] != "times") { return 41; }

    // len() is still BYTES; segmentation changed nothing about that.
    if ("café".len() != 5) { return 42; }
    return 0;
}
`

func TestWordsInterp(t *testing.T) {
	if got := runInterpExit(t, wordsProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestWordsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, wordsProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestWordsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, wordsProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestWordsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, wordsProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
