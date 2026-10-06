// The differential oracles' shared pieces: the interpreter baseline every
// generated program is judged against, the seed window and shard every sweep
// draws from. The exit-byte sweeps
// themselves are diff_oracle_selfhost_test.go and its arm64 and wasm
// siblings; FuzzGenerate_ExecutionAgrees below is their coverage-guided form.
package e2e

import (
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

// diffOracleShard reads the optional `DIFF_ORACLE_SHARD` env var,
// expected as "I/N" with 0 <= I < N (e.g. "0/4", "3/4"). Returns
// (I, N) on success and (0, 1) — the full-sweep identity — when
// the var is unset. Malformed values t.Fatal so a CI misconfig
// surfaces immediately rather than silently running one shard's
// worth of seeds across every job.
func diffOracleShard(t *testing.T) (uint64, uint64) {
	t.Helper()
	raw := os.Getenv("DIFF_ORACLE_SHARD")
	if raw == "" {
		return 0, 1
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: want I/N (e.g. 0/4)", raw)
	}
	idx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: index parse: %v", raw, err)
	}
	count, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: count parse: %v", raw, err)
	}
	if count == 0 || idx >= count {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: require 0 <= I < N and N > 0", raw)
	}
	return idx, count
}

// diffOracleSeedBase reads the optional `DIFF_ORACLE_SEED_BASE` env
// var: the first seed of the sweep, defaulting to 0. The nightly
// workflow derives it from the run number so each night sweeps a
// window the fixed PR corpus never reaches, without any committed
// cursor — the window is a function of the run, so any past night's
// range is recomputable and re-runnable with one env var.
//
// The seed-keyed known-divergence tables in this package only
// describe base-0 programs; a shifted window simply never matches
// them, which is what you want — a row names one specific program,
// not an index.
func diffOracleSeedBase(t *testing.T) uint64 {
	t.Helper()
	raw := os.Getenv("DIFF_ORACLE_SEED_BASE")
	if raw == "" {
		return 0
	}
	base, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SEED_BASE=%q: parse: %v", raw, err)
	}
	return base
}

// diffOracleWindow returns the seeds this run should sweep: count
// seeds from the base, keeping the ones this shard claims
// (`seed % N == I`). Every fernsmith seed sweep in the package goes
// through here, so the base and the shard split are defined once.
//
// The shard filter is applied to the absolute seed, so a given seed
// lands on the same shard whatever the base is — a failure found in
// shard 1 of a nightly window reproduces in shard 1 of a re-run.
func diffOracleWindow(t *testing.T, count uint64) []uint64 {
	t.Helper()
	base := diffOracleSeedBase(t)
	shardIdx, shardCount := diffOracleShard(t)
	seeds := make([]uint64, 0, count/shardCount+1)
	for i := uint64(0); i < count; i++ {
		seed := base + i
		if seed%shardCount != shardIdx {
			continue
		}
		seeds = append(seeds, seed)
	}
	return seeds
}

// FuzzGenerate_ExecutionAgrees drives fernsmith from the fuzzer's byte stream
// and asserts each self-host target's exit byte matches the interpreter's. A
// leg whose toolchain is missing skips, so the fuzzer stresses a target only
// where that target can run.
//
// Run with: go test -fuzz=FuzzGenerate_ExecutionAgrees -run=^$ ./internal/testing/e2e
func FuzzGenerate_ExecutionAgrees(f *testing.F) {
	// Seed with a handful of deterministic uint64 seeds repackaged
	// as 8-byte slices so the corpus is non-empty on first run;
	// after that the mutator drives the byte stream directly.
	for seed := uint64(0); seed < 16; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	e2eharness.WarmSelfHostCLI(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		src := fernsmith.GenMainBytes(data)
		expected := runInterpByteOrSkip(t, src)
		t.Run("arm64-linux", func(t *testing.T) {
			_, code := compileAndRunArm64(t, src)
			if code != expected {
				t.Errorf("arm64 exit=%d, interp=%d\ndata=%x\nsrc:\n%s", code, expected, data, src)
			}
		})
		t.Run("x86_64", func(t *testing.T) {
			_, code := compileAndRunX86_64(t, src)
			if code != expected {
				t.Errorf("x86_64 exit=%d, interp=%d\ndata=%x\nsrc:\n%s", code, expected, data, src)
			}
		})
		t.Run("wasm32-wasi", func(t *testing.T) {
			got := compileAndRunWasmbinMain(t, src)
			if got != expected {
				t.Errorf("wasm result=%d, interp=%d\ndata=%x\nsrc:\n%s", got, expected, data, src)
			}
		})
	})
}

// TestInterpOracleRunsGenericBodies pins the oracle's reach over generic
// code. A body bound `T: cmp.Eq` compares with the bound's `eq` method, and
// without monomorph that reaches the interpreter as a field access on a
// number — which used to `t.Skipf`, so every generic-bodied case in the
// differentials passed vacuously (#6840).
//
// The assertion is written as an inner sub-test whose completion is recorded,
// so a reintroduced skip fails the parent instead of turning it green again.
func TestInterpOracleRunsGenericBodies(t *testing.T) {
	src := `import "core/cmp" as cmp;
function same[T: cmp.Eq](a: T, b: T): boolean { return a.eq(b); }
function main(): i32 {
    let r: i32 = 0;
    if (same(1, 1)) { r = r + 1; }
    if (!same(2, 3)) { r = r + 2; }
    if (same("a", "a")) { r = r + 4; }
    if (!same("a", "b")) { r = r + 8; }
    return r;
}`
	ran := false
	t.Run("Eq-bounded body", func(t *testing.T) {
		if got := runInterpByte(t, src); got != 15 {
			t.Errorf("interp: got exit %d, want 15", got)
		}
		ran = true
	})
	if !ran {
		t.Fatal("the interp oracle skipped a generic body: it is not running the differential suites' generic cases (#6840)")
	}
}

// runInterpByte parses + checks + monomorphises + runs `main()`
// under the in-process interpreter and returns the result masked
// to a byte. Sources from `fernsmith.GenMain` already mask to a
// byte; the extra `& 0xFF` here is defensive in case the harness
// is ever fed a program that doesn't.
//
// An interpreter error FAILS the test. The interpreter is the
// differential oracle's source of truth, so a program it cannot
// run has to be visible: a skip here takes the compiled backends
// down with it and leaves the parent reporting PASS with nothing
// asserted (#6840). Hand-written cases therefore use this;
// generator-driven corpora use runInterpByteOrSkip.
func runInterpByte(t *testing.T, src string) int {
	t.Helper()
	return interpByte(t, src, false)
}

// runInterpByteOrSkip is runInterpByte for the fernsmith corpora,
// where interp-side coverage gaps (closures, the `?` propagation
// operator, etc.) `t.Skipf` rather than fail — the interpreter
// isn't a feature-complete target, and the generator regularly
// emits programs it doesn't model.
func runInterpByteOrSkip(t *testing.T, src string) int {
	t.Helper()
	return interpByte(t, src, true)
}

// interpByte is the shared body. Parser / checker / monomorph
// errors always Fatal: for a generated program those would mean
// fernsmith produced something the front end shouldn't have
// accepted, and for a hand-written one they are the bug.
func interpByte(t *testing.T, src string, skipGaps bool) int {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v\nsrc:\n%s", err, src)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v\nsrc:\n%s", err, src)
	}
	// Without monomorph a generic body reaches the interpreter with an
	// unsubstituted bound, so a `T: cmp.Eq` call like `x.eq(y)` looks like a
	// field access on a number. Every compiled backend runs this pass.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v\nsrc:\n%s", err, src)
	}
	i := interp.New()
	i.SetDynCoercions(info.DynCoercions)
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	// `exit(code)` must be captured, not run as a real os.Exit (which would
	// kill the whole test binary). The interp expects a non-returning Exiter
	// substitute, so panic with the code and recover it here — the captured
	// code wins over main's return value, matching process semantics.
	type interpExit struct{ code int }
	i.Exiter = func(code int) { panic(interpExit{code}) }
	var v interp.Value
	exitCode := -1
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ie, ok := r.(interpExit); ok {
					exitCode = ie.code
					return
				}
				panic(r)
			}
		}()
		v, err = i.CallByName("main", nil)
	}()
	if exitCode >= 0 {
		return exitCode & 0xFF
	}
	if err != nil {
		if skipGaps {
			t.Skipf("interp coverage gap: %v", err)
		}
		t.Fatalf("interp: %v\nsrc:\n%s", err, src)
	}
	n, ok := v.(interp.Number)
	if !ok {
		if skipGaps {
			t.Skipf("interp main returned non-number %T (coverage gap)", v)
		}
		t.Fatalf("interp: main returned %T, want a number\nsrc:\n%s", v, src)
	}
	return int(int64(n) & 0xFF)
}
