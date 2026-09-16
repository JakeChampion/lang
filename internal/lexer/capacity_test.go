package lexer

import (
	"os"
	"path/filepath"
	"testing"
)

// The token slice is sized at one token per 7 source bytes, from the density of
// the repository's own Fern sources. This pins that population: a single dense
// file runs far tighter than the average (code with no comments or long string
// literals reaches one token per 2.5 bytes), so the reserve is a starting point
// rather than a bound, and the guard is that the corpus as a whole still sits
// on the far side of the divisor.
func TestTokenSliceIsSizedForTheCorpusDensity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "self_host", "*.fern"))
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
			continue // a source the lexer rejects is not this test's business
		}
		bytes += len(src)
		tokens += len(toks)
	}
	if tokens == 0 {
		t.Fatal("no tokens across the corpus")
	}
	density := float64(bytes) / float64(tokens)
	if density < 7 {
		t.Errorf("corpus density is one token per %.2f bytes over %d files, tighter than the 7 the slice reserves for", density, len(files))
	}
}
