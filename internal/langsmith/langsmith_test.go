package langsmith_test

import (
	"encoding/binary"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/langsmith"
	"github.com/jakechampion/lang/internal/parser"
)

// randBytes fills out with n bytes drawn from r. math/rand/v2 dropped
// the v1 *rand.Rand.Read method, so the tests roll their own.
func randBytes(r *rand.Rand, n int) []byte {
	out := make([]byte, n)
	for i := 0; i+8 <= n; i += 8 {
		binary.LittleEndian.PutUint64(out[i:], r.Uint64())
	}
	if tail := n % 8; tail != 0 {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], r.Uint64())
		copy(out[n-tail:], buf[:tail])
	}
	return out
}

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

// TestGenMainProducesRunnablePrograms — every seed yields a
// well-typed program whose `main(): i32` returns a byte. The
// load-bearing invariant of the differential-oracle harness in
// internal/e2e.
func TestGenMainProducesRunnablePrograms(t *testing.T) {
	for seed := uint64(0); seed < 256; seed++ {
		src := langsmith.GenMain(seed)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("seed=%d: missing main\nsrc:\n%s", seed, src)
			continue
		}
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("seed=%d failed to parse:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("seed=%d failed to type-check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

// TestGenBytesProducesValidPrograms — byte-stream API mirror of
// TestGenProducesValidPrograms. Uses pseudo-random byte slabs of
// varying lengths so the test exercises both byte-rich (mutator
// drives every decision) and byte-poor (early exhaustion → short
// program) regimes.
func TestGenBytesProducesValidPrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 99))
	for i := 0; i < 64; i++ {
		n := r.IntN(512) // 0..511 bytes; covers exhaustion path too
		data := randBytes(r, n)
		src := langsmith.GenBytes(data)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("i=%d len=%d failed to parse:\nsrc:\n%s\nerr: %v", i, n, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("i=%d len=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, n, src, err)
		}
	}
}

// TestGenMainBytesProducesRunnablePrograms — byte-stream API
// mirror for GenMain. Same exhaustion-coverage rationale.
func TestGenMainBytesProducesRunnablePrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 13))
	for i := 0; i < 64; i++ {
		n := r.IntN(512)
		data := randBytes(r, n)
		src := langsmith.GenMainBytes(data)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("i=%d: missing main\nsrc:\n%s", i, src)
			continue
		}
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("i=%d failed to parse:\nsrc:\n%s\nerr: %v", i, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("i=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, src, err)
		}
	}
}

// TestGenBytesIsDeterministic — same bytes → same source. The
// minimisation contract relies on this: a smaller corpus must
// produce a smaller-or-same program, not a different one.
func TestGenBytesIsDeterministic(t *testing.T) {
	for _, n := range []int{0, 1, 8, 64, 256} {
		r := rand.New(rand.NewPCG(uint64(n), 31415))
		data := randBytes(r, n)
		a := langsmith.GenBytes(data)
		b := langsmith.GenBytes(data)
		if a != b {
			t.Errorf("len=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", n, a, b)
		}
	}
}

// TestGenBytesExhaustionShrinksProgram — chopping bytes off the
// end of a corpus shouldn't make the emitted program *longer*.
// This is the load-bearing minimisation property: the fuzzer's
// shrinker truncates and shuffles bytes, and a working
// generator turns shorter input into shorter (or equal) source.
func TestGenBytesExhaustionShrinksProgram(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	full := randBytes(r, 256)
	long := langsmith.GenBytes(full)
	short := langsmith.GenBytes(full[:8]) // one decision's worth
	empty := langsmith.GenBytes(nil)
	if len(short) > len(long) {
		t.Errorf("short corpus produced longer program: %d > %d", len(short), len(long))
	}
	if len(empty) > len(short) {
		t.Errorf("empty corpus produced longer program: %d > %d", len(empty), len(short))
	}
}
