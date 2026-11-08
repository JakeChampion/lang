package e2ecompiler

import (
	"os"
	"strings"
	"testing"
)

// The targets here feed hostile bytes to the literate reader through
// drivers/literate_fuzz_run.fern. A package can ship `.fern.md` modules, so
// the chunk grammar, the tangle and the weave read bytes an attacker
// controls. The reader refuses a malformed document with a message, so the
// one accepted outcome is exit 0 with a summary; a signal, a hang or a
// budget breach fails the input.

func literateSeeds(t testing.TB) [][]byte {
	doc, err := os.ReadFile("testdata/weave/doc.fern.md")
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{
		doc,
		[]byte("# One chunk\n\n```fern\n<<*>>=\nfunction main(): i32 {\n  <<body>>\n  return 0;\n}\n```\n\n```fern\n<<body>>=\nlet x = 1;\n```\n"),
		[]byte("```fern file=main.fern entry\n<<shared>>\nfunction main(): i32 { return s(); }\n```\n\n```fern file=lib.fern\n<<shared>>\n```\n\n```fern\n<<shared>>=\npub function s(): i32 { return 1; }\n```\n"),
		[]byte("```fern\n<<*>>=\n<<missing>>\n```\n"),
		[]byte("```fern\n<<*>>=\n<<a>>\n```\n\n```fern\n<<a>>=\n<<b>>\n```\n\n```fern\n<<b>>=\n<<a>>\n```\n"),
		[]byte("```fern\n<<*>>=\n<<*>>\n```\n"),
		[]byte("```fern\n<<*>>=\n\\<<literal>>\n  <<indented>>\n```\n\n```fern\n<<indented>>=\nline one\nline two\n```\n"),
		[]byte("```fern\n<<*>>=\nfunction main(): i32 { return 0; }\n"),
		[]byte("```fern\n<<>>=\n```\n\n```fern\n<<a b>>=\n<< >>\n```\n"),
		[]byte("```fern file=\n```\n\n```fern file=../escape.fern\n```\n\n```fern file=a.fern entry\n```\n\n```fern file=a.fern entry\n```\n"),
		[]byte("```fern test name=adds\nfunction main(): i32 { return 0; }\n```\n\n```fern test\n```\n"),
		[]byte(strings.Repeat("```fern\n<<c>>=\n<<c>>\n```\n", 200)),
		[]byte("```fern\n<<*>>=\n" + strings.Repeat("<<d>>\n", 3000) + "```\n\n```fern\n<<d>>=\nx\n```\n"),
		[]byte("```fern\n<<*>>=\n" + strings.Repeat("a", 60000) + "\n```\n"),
		[]byte("# \xff\xfe\n\n```fern\n<<*>>=\n\xc3\x28\n```\n"),
		[]byte(""),
		[]byte("````\n```fern\n<<*>>=\n```\n````\n"),
	}
}

// FuzzSelfHostTangle feeds hostile documents to the chunk grammar, the
// tangle of the root chunk and of the file roots, and the unused-chunk lint.
func FuzzSelfHostTangle(f *testing.F) {
	fuzzReader(f, "literate_fuzz_run", "tangle", literateSeeds(f))
}

// FuzzSelfHostWeave feeds hostile documents to the weave, to text and to HTML.
func FuzzSelfHostWeave(f *testing.F) {
	fuzzReader(f, "literate_fuzz_run", "weave", literateSeeds(f))
}
