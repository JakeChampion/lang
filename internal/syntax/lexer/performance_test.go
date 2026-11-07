package lexer

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestTokenLayout(t *testing.T) {
	if size := unsafe.Sizeof(Token{}); size > 48 {
		t.Fatalf("Token occupies %d bytes, want at most 48", size)
	}
}

func TestTokenizeStringRuns(t *testing.T) {
	for n := 0; n <= 80; n++ {
		prefix := strings.Repeat(" ", n)
		text := strings.Repeat("x", n) + "é\x00"
		for _, escaped := range []bool{false, true} {
			raw, want := text, text
			if escaped {
				raw += `\n\"\u{1F600}` + text
				want += "\n\"😀" + text
			}
			src := prefix + `"` + raw + `";`
			tokens, _, err := Tokenize(src)
			if err != nil {
				t.Fatalf("n=%d escaped=%v: %v", n, escaped, err)
			}
			if len(tokens) != 3 || tokens[0].Kind != String || tokens[0].Text != want || tokens[0].Pos.Col != n+1 || tokens[1].Pos.Col != len(src) {
				t.Fatalf("n=%d escaped=%v: %+v", n, escaped, tokens)
			}
		}
	}
}

func TestTokenizeCommentRuns(t *testing.T) {
	for n := 0; n <= 80; n++ {
		text := strings.Repeat("x", n) + "é\x00\r"
		for _, newline := range []bool{false, true} {
			src := "x; //" + text
			wantEOF := ast.Position{Line: 1, Col: len(src) + 1}
			if newline {
				src += "\ny"
				wantEOF = ast.Position{Line: 2, Col: 2}
			}
			tokens, comments, err := Tokenize(src)
			if err != nil {
				t.Fatal(err)
			}
			if len(comments) != 1 || comments[0].Text != text || comments[0].Pos != (ast.Position{Line: 1, Col: 4}) || !comments[0].Trailing {
				t.Fatalf("n=%d newline=%v: %+v", n, newline, comments)
			}
			if got := tokens[len(tokens)-1].Pos; got != wantEOF {
				t.Fatalf("n=%d newline=%v: EOF at %v, want %v", n, newline, got, wantEOF)
			}
		}
	}
}

func BenchmarkTokenizePayloads(b *testing.B) {
	for name, src := range map[string]string{
		"strings":  strings.Repeat(`"plain é text" "escaped\ntext"; `, 256),
		"comments": strings.Repeat("//"+strings.Repeat("x", 4096)+"\nlet x = 1;\n", 16),
		"suffixes": strings.Repeat("1i32 2i64 3u8 4u32 5u64 6usize 7f32 8f64 ", 256),
		"fstrings": strings.Repeat(`f"a{x}b" `, 256),
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))
			for i := 0; i < b.N; i++ {
				if _, _, err := Tokenize(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
