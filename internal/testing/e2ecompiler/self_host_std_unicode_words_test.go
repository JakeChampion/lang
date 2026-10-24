package e2ecompiler

import "testing"

const unicodeWordsModMain = `import "std/unicode" as unicode;
function main(): i32 {
    let s: string = "Hello, world! 42 times.";
    return unicode.word_count(s) * 10 + unicode.word_segments(s).len();
}
`

func TestSelfHostStdUnicodeWordsX86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "unicodewordsprog", unicodeWordsModMain, 50)
}
