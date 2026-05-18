package langsmith_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/langsmith"
	"github.com/jakechampion/lang/internal/parser"
)

// TestGenProducesValidPrograms is the deterministic counterpart to
// FuzzGenerate_ParseRoundTrips — it walks a fixed range of seeds so
// regressions show up in `go test ./...` without anyone having to
// remember to run the fuzzer. Failure prints the failing source so
// the seed-to-input mapping is reproducible.
func TestGenProducesValidPrograms(t *testing.T) {
	for seed := uint64(0); seed < 256; seed++ {
		src := langsmith.Gen(seed)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("seed=%d failed to parse:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("seed=%d failed to type-check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

// TestGenIsDeterministic — same seed → same source, every time.
// Without this the corpus for the fuzzer can't be minimised
// reproducibly and a reported crash can't be replayed.
func TestGenIsDeterministic(t *testing.T) {
	for _, seed := range []uint64{0, 1, 42, 1234567} {
		a := langsmith.Gen(seed)
		b := langsmith.Gen(seed)
		if a != b {
			t.Errorf("seed=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", seed, a, b)
		}
	}
}

// TestGenEmitsAtLeastOneFunction — the load-bearing claim of v1 is
// "non-empty, well-typed program". Tighten the guarantee here.
func TestGenEmitsAtLeastOneFunction(t *testing.T) {
	for seed := uint64(0); seed < 16; seed++ {
		src := langsmith.Gen(seed)
		if !strings.Contains(src, "function gen_f0(") {
			t.Errorf("seed=%d: missing gen_f0 in output\nsrc:\n%s", seed, src)
		}
	}
}
