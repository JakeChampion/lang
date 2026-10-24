package lexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenSliceIsSizedForTheCorpusDensity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "compiler", "*.fern"))
	inDrivers, _ := filepath.Glob(filepath.Join("..", "..", "..", "compiler", "drivers", "*.fern"))
	files = append(files, inDrivers...)
	if err != nil || len(files) == 0 {
		t.Skipf("no self-host sources to measure: %v", err)
	}
	var bytes, tokens int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		toks, _, err := Tokenize(string(src))
		if err != nil {
			continue
		}
		bytes += len(src)
		tokens += len(toks)
	}
	if tokens == 0 {
		t.Fatal("no tokens across the corpus")
	}
	density := float64(bytes) / float64(tokens)
	t.Logf("corpus: %d bytes, %d tokens, %d files, %.6f bytes/token", bytes, tokens, len(files), density)
	if density < tokenSourceBytesPerSlot {
		t.Errorf("corpus density is one token per %.2f bytes over %d files, tighter than the %d the slice reserves for", density, len(files), tokenSourceBytesPerSlot)
	}
}

func BenchmarkTokenizeSelfHostCorpus(b *testing.B) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "compiler", "*.fern"))
	if err != nil {
		b.Fatal(err)
	}
	drivers, err := filepath.Glob(filepath.Join("..", "..", "..", "compiler", "drivers", "*.fern"))
	if err != nil {
		b.Fatal(err)
	}
	files = append(files, drivers...)
	var sources []string
	var bytes int64
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := Tokenize(string(src)); err != nil {
			continue
		}
		sources = append(sources, string(src))
		bytes += int64(len(src))
	}
	if len(sources) == 0 {
		b.Fatal("no tokenizable self-host sources")
	}
	b.ReportAllocs()
	b.SetBytes(bytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, src := range sources {
			if _, _, err := Tokenize(src); err != nil {
				b.Fatal(err)
			}
		}
	}
}
