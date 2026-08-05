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

// printableStdoutSeeds is the deterministic sweep size. Each seed
// compiles + runs across three external backends (gcc/qemu ×2 +
// wasmtime), so it is not free; drops to 1/8th under -short for the
// dev loop.
//
// Raised 256 → 1024 once the sweep started honouring DIFF_ORACLE_SHARD
// (below), which makes the increase cost-NEUTRAL per CI cell: 256 seeds
// unsharded measured 47s, and 1024 split four ways is the same 256 per
// shard. Before that every one of the differential workflow's eight
// cells ran the identical 256 seeds — the same work eight times.
//
// 256 was demonstrably too narrow. #5796 — a generic-inference bug that
// rejected a valid program — lives at seed 603, and this oracle
// t.Fatals on a checker error by design, so the corpus could not even
// reach it. Seeds 0..2047 are verified clean on x86-64 and on wasm, so
// 2048 is available if the per-cell budget ever allows.
const printableStdoutSeeds = 1024

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
//
// Honours DIFF_ORACLE_SHARD like the exit-byte oracle, so the four
// shards of each arch split the corpus instead of each running all of
// it.
// printableKnownDivergences maps a seed to the backends that are known
// to be wrong on it, each against its tracking issue. Scoped per backend
// so the ones that agree still run — their agreement is what says the
// skipped backend has a bug rather than the generated program being
// invalid.
//
// A row is a known COMPILER bug being tolerated so the rest of the
// corpus keeps running. The skip is loud and cites an open issue, so an
// untracked row is visible as such.
var printableKnownDivergences = map[uint64]map[string]string{
	// The WAT-text wasm backend aborts (wasmtime exit 134) on these two
	// seeds' closure shapes; x86-64 and arm64 agree with interp on both.
	// Distinct backend from the wasmbin trap in #6142, same generator
	// productions reached it.
	545: {"wasm": "https://github.com/JakeChampion/lang/issues/6145"},
	831: {"wasm": "https://github.com/JakeChampion/lang/issues/6145"},
}

func TestDifferential_PrintableStdout(t *testing.T) {
	shardIdx, shardCount := diffOracleShard(t)
	seedCount := printableSeeds(t)
	for seed := uint64(0); seed < seedCount; seed++ {
		if seed%shardCount != shardIdx {
			continue
		}
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			assertNumProgramAgreesSkipping(t, fernsmith.GenPrintableMain(seed), printableKnownDivergences[seed])
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
