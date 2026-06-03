// Stdout differential oracle. The sibling diff_oracle_test.go compares
// only main()'s 1-byte return code, which can't observe floats or
// strings — so the fernsmith ProfileRunnable path excludes them. This
// oracle closes that gap: fernsmith's ProfilePrintable emits a `main`
// that PRINTS a sequence of computed values (then returns 0), and the
// harness compares the full stdout across interp / x86-64 / arm64 /
// wasm.
//
// Float values are observed only through portable channels — boolean
// comparisons ("T"/"F", including NaN/Inf-aware ones) and truncating
// `as i32` casts — never raw decimal formatting, whose digit count
// Lang deliberately under-specifies (docs/FLOAT-SEMANTICS.md). That
// keeps the oracle a clean signal: a stdout mismatch is a real codegen
// divergence, not a formatting artifact. This is the class of bug that
// `TestBugHunt_FloatNaNComparisonParity` was added for — the x86-64
// unordered-comparison bug would have shown up here as a "T"/"F"
// divergence on a NaN comparison.
//
// Reuses assertNumProgramAgrees from numeric_property_test.go (interp
// is the source of truth; each backend runs in its own sub-test and
// skips individually when its toolchain is missing).
package e2e

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/fernsmith"
)

// printableStdoutSeeds is the deterministic sweep size. Kept modest
// because each seed compiles + runs across three external backends
// (gcc/qemu ×2 + wasmtime); the fuzz target below expands the search
// on demand. Drops to 1/8th under -short for the dev loop.
const printableStdoutSeeds = 256

func printableSeeds(t *testing.T) uint64 {
	t.Helper()
	if testing.Short() {
		return printableStdoutSeeds / 8
	}
	return printableStdoutSeeds
}

// TestDifferential_PrintableStdout is the deterministic seeded sweep:
// for each seed, generate a printable `main` and assert every backend's
// stdout matches the interpreter's. Per-seed subtests run in parallel.
func TestDifferential_PrintableStdout(t *testing.T) {
	seedCount := printableSeeds(t)
	for seed := uint64(0); seed < seedCount; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			assertNumProgramAgrees(t, fernsmith.GenPrintableMain(seed))
		})
	}
}

// FuzzGenerate_StdoutAgrees drives the printable generator from the
// fuzzer's byte stream (so corpus minimisation shrinks programs
// monotonically) and asserts cross-backend stdout agreement. The
// stdout counterpart of FuzzGenerate_ExecutionAgrees.
func FuzzGenerate_StdoutAgrees(f *testing.F) {
	for seed := uint64(0); seed < 16; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		assertNumProgramAgrees(t, fernsmith.GenPrintableMainBytes(data))
	})
}
