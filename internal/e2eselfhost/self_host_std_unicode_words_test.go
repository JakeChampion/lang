package e2eselfhost

import "testing"

// UAX #29 word segmentation (#5552) is pure Fern on top of a generated
// static table, so the native differential in internal/e2e is where its
// boundary rules are pinned. This gate answers the other question: does
// the self-hosted compiler LOWER it, not merely type-check it.
//
// That distinction is the #6018 lesson — a module can pass every
// type-level gate and still be miscompiled — and it is why this asserts
// a runtime result rather than a successful build. std/string imports
// std/unicode, so the module is already checked on every self-host run;
// nothing before this ran its segmentation code.
//
// "Hello, world! 42 times." → 4 words (Hello / world / 42 / times) and
// 10 segments once the punctuation and spaces are counted, so the
// program returns 4*10 + 10 = 50. Both numbers come from the same
// `uniseg` oracle the table data does.
const unicodeWordsModMain = `import "std/unicode" as unicode;
function main(): i32 {
    var s: string = "Hello, world! 42 times.";
    return unicode.word_count(s) * 10 + unicode.word_segments(s).len();
}
`

func TestSelfHostStdUnicodeWordsX86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "unicodewordsprog", unicodeWordsModMain, 50)
}
