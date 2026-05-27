package fernsmith_test

import (
	"encoding/binary"
	"testing"

	"github.com/jakechampion/lang/internal/fernsmith"
)

// FuzzGenerate_ParseRoundTrips is the wasm-smith-style oracle: every
// generated program must parse AND type-check. Drives the byte-
// stream generator directly so testing.F's mutator (which shrinks /
// grows / flips bytes) maps each edit onto a small contiguous
// chunk of generator decisions — exactly the corpus-minimisation
// property `wasm-smith` relies on for shrinking failing inputs.
//
// Run with: go test -fuzz=FuzzGenerate_ParseRoundTrips ./internal/fernsmith
func FuzzGenerate_ParseRoundTrips(f *testing.F) {
	// Seed with a handful of deterministic uint64 seeds repackaged
	// as 8-byte slices. The fuzzer will mutate around these.
	for seed := uint64(0); seed < 32; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		src := fernsmith.GenBytes(data)
		// modload (not bare parser.Parse) resolves the stdlib import
		// preamble the generator emits now that the prelude is gone.
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("generated program failed to parse/type-check:\ndata=%x\nsrc:\n%s\nerr: %v", data, src, err)
		}
	})
}
