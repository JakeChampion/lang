package langsmith_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/langsmith"
	"github.com/jakechampion/lang/internal/parser"
)

// FuzzGenerate_ParseRoundTrips is the wasm-smith-style oracle: every
// generated program must parse AND type-check. The byte-mutation
// fuzzers in internal/parser and internal/checker hit the front end
// with mostly junk; this one drives the generator with random seeds
// so the inputs the parser sees are valid by construction, which
// means any error or panic from parser/checker is a real bug, not a
// rejection of malformed source.
//
// Run with: go test -fuzz=FuzzGenerate_ParseRoundTrips ./internal/langsmith
func FuzzGenerate_ParseRoundTrips(f *testing.F) {
	for seed := uint64(0); seed < 32; seed++ {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		src := langsmith.Gen(seed)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("generated program failed to parse:\nseed=%d\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("generated program failed to type-check:\nseed=%d\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	})
}
