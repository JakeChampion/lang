package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/fernsmith"
)

// selfHostWasmDiffKnownFile and selfHostSemWasmDiffKnownFile list the seeds
// whose self-host wasm result disagrees with the interpreter's, one file per
// lowering, same contract as the x86-64 and arm64 pairs: a listed seed that
// starts passing fails too.
const selfHostWasmDiffKnownFile = "selfhost-diff-wasm-known-divergences.txt"
const selfHostSemWasmDiffKnownFile = "selfhost-diff-semantic-wasm-known-divergences.txt"

// selfHostWasmDiffMinRunRatio is this leg's own floor on seeds that compiled
// and produced a COMPARABLE answer, lower than selfHostDiffMinRunRatio because
// two endpoints reduce it here rather than one. A seed can be dropped by a
// compile gap, as on every leg, and it can also be dropped by the WASI exit
// clamp below, which no other leg has. Measured 2026-09-16 over seeds 0..63:
// 34 compared, 27 of the remainder uncomparable under the clamp (0.53). The
// floor sits under that with room for the corpus to shift, and is a ratchet to
// raise: the clamp is a property of WASI, so the way to move it is to give the
// emit somewhere other than the exit status to put main's answer.
const selfHostWasmDiffMinRunRatio = 0.45

// TestDifferential_SelfHostWasm runs the fernsmith corpus through the
// self-host wasm emitter and asserts the module's exit matches the
// interpreter's, the third backend's entry in a family that had only two.
//
// # Why wasm was missing, and why that mattered
//
// The x86-64 and arm64 legs cover the two register backends. Wasm is a
// different emitter over a different string and map layout — its strings are
// inline blocks with no data pointer, its map is a hash table rather than the
// register backends' parallel arrays, and the semantic lowering reaches a
// column's release through a funcref table slot where the register backends
// pass a code address. None of that had a generated program pointed at it.
//
// # The WASI exit clamp, and why a seed can be uncomparable
//
// WASI refuses an exit status outside [0..126): a module returning 126 or more
// exits 1 with "invalid exit status", so the program's answer is invisible.
// The self-host wasm emit has no equivalent of the native wasm path's
// fold-main's-result-into-stdout, so there is nothing else to read it from.
//
// A seed whose oracle byte is >= 126 therefore cannot have its VALUE checked.
// It is not skipped: a trap or a rejected module is a real failure whatever
// the program meant to return, and those are still asserted. Only the number
// comparison is dropped, and the seed is counted out of the run ratio so the
// floor measures what was actually compared.
func TestDifferential_SelfHostWasm(t *testing.T) {
	testDifferentialSelfHostWasm(t, false)
}

// TestDifferential_SelfHostSemanticWasm is the same corpus through the same
// emitter with the SEMANTIC lowering. Both legs spell FERN_SEM_IR, for the
// reason the x86-64 pair's compile step gives.
func TestDifferential_SelfHostSemanticWasm(t *testing.T) {
	testDifferentialSelfHostWasm(t, true)
}

func testDifferentialSelfHostWasm(t *testing.T, semantic bool) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// Cannot exec the x86-64 driver, so there is no compiler to run.
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the self-host wasm differential leg")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	knownFile := selfHostWasmDiffKnownFile
	if semantic {
		knownFile = selfHostSemWasmDiffKnownFile
	}
	known := loadKnownDivergences(t, knownFile)

	var sampled, compared, clamped int64
	for _, seed := range diffOracleWindow(t, selfHostDiffSeeds(t)) {
		seed := seed
		sampled++
		key := strconv.FormatUint(seed, 10)
		reason, isKnown := known[key]
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			want := runInterpByteOrSkip(t, src)

			diverged := false
			failf := func(format string, args ...any) {
				if isKnown {
					diverged = true
					t.Logf("known divergence (%s): "+format, append([]any{reason}, args...)...)
					return
				}
				t.Errorf(format, args...)
			}

			out, exit, gap, report := runSelfHostWasmSeed(t, fernBin, stdlibRoot, src, semantic)
			if gap != "" {
				// Same contract as the other legs: a compile bail is a
				// documented endpoint for an unlisted seed, but a LISTED one
				// that no longer compiles cannot demonstrate the wrong answer
				// its row claims, so the row is stale and someone must look.
				if !isKnown {
					t.Skipf("self-host wasm coverage gap: %s", gap)
				}
				t.Errorf("seed %d is listed in testdata/%s (%s) but it no longer COMPILES, so the row "+
					"cannot be verified — re-check it and either update the reason or delete it:\n%s",
					seed, knownFile, reason, gap)
				return
			}
			if semantic {
				requireSemWhole(t, report)
			}

			// A rejected module and a trap are failures whatever the oracle
			// byte is, so both are asserted before the clamp is consulted.
			if rejected, why := wasmRejected(out); rejected {
				failf("wasmtime REJECTED the self-host module — not a wrong answer, the artifact is "+
					"invalid or incomplete (%s):\n%s\nsrc:\n%s", why, out, src)
				return
			}
			if strings.Contains(out, "wasm trap:") {
				failf("the self-host wasm module TRAPPED, which the interpreter's %d does not:\n%s\nsrc:\n%s",
					want, out, src)
				return
			}
			if want >= 126 {
				// Uncomparable, not wrong: see the leg's header. Counted so
				// the ratio below measures what was compared.
				atomic.AddInt64(&clamped, 1)
				return
			}
			atomic.AddInt64(&compared, 1)
			if exit != want {
				failf("self-host wasm exit = %d, interpreter = %d\n%s\nsrc:\n%s", exit, want, out, src)
			}

			if isKnown && !diverged {
				t.Errorf("seed %d is listed in testdata/%s (%s) but it AGREES now — delete the entry",
					seed, knownFile, reason)
			}
		})
	}

	t.Cleanup(func() {
		got := atomic.LoadInt64(&compared)
		if sampled == 0 {
			return
		}
		t.Logf("compared %d of %d sampled seeds (%d uncomparable under the WASI exit clamp)",
			got, sampled, atomic.LoadInt64(&clamped))
		if ratio := float64(got) / float64(sampled); ratio < selfHostWasmDiffMinRunRatio {
			t.Errorf("only %d of %d sampled seeds were COMPARED (%.2f) — below the %.2f floor. "+
				"Compile gaps and the exit clamp are documented endpoints, but at this rate the leg is "+
				"not testing the compiler", got, sampled, ratio, selfHostWasmDiffMinRunRatio)
		}
	})
}

// runSelfHostWasmSeed compiles src for wasm32-wasi with the self-host CLI and
// runs the module under wasmtime. Returns (combined output, exit, "", report)
// when it ran and ("", 0, gap, report) when the compiler bailed; the report is
// the compiler's stderr, which carries the production tally the semantic leg
// reads.
func runSelfHostWasmSeed(t *testing.T, fernBin, stdlibRoot, src string, semantic bool) (string, int, string, string) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	watPath := filepath.Join(dir, "prog.wat")
	compile := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot, "-o", watPath)
	// Spelled EMPTY on the control leg rather than left unset, for the reason
	// the x86-64 pair gives: empty is off, and writing it is what stops an
	// ambient FERN_SEM_IR in the environment turning both legs into the
	// semantic one.
	compile.Env = append(os.Environ(), "FERN_SEM_IR=", "FERN_SEM_IR_STRICT=", "FERN_SEM_IR_REPORT=1")
	if semantic {
		compile.Env = append(compile.Env, "FERN_SEM_IR=1")
	}
	out, err := compile.CombinedOutput()
	report := string(out)
	if err != nil {
		return "", 0, fmt.Sprintf("%v\n%s%s", err, out,
			strictIRBailSite(fernBin, "wasm32-wasi", []string{"-emit", "asm"}, srcPath, stdlibRoot, out)), report
	}
	run := runSelfHostBin(exec.Command("wasmtime", "run", watPath), "")
	return run.stdout + run.stderr, run.exit, "", report
}
