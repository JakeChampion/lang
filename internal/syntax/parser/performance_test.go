package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkParseCompilerLexer(b *testing.B) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "lexer.fern"))
	if err != nil {
		b.Fatal(err)
	}
	text := string(src)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for i := 0; i < b.N; i++ {
		if _, err := Parse(text); err != nil {
			b.Fatal(err)
		}
	}
}
