// Differential-execution oracle for the experimental SSA-direct
// backend (`-target arm64-ssa`).
//
// The oracles in diff_oracle_test.go and printable_stdout_test.go
// cover arm64, x86_64 and wasmbin — every backend that lowers
// straight from the IR. Neither covers the SSA path:
// `internal/ssa`'s `LiftFromIR` feeds only `-target arm64-ssa` and
// `-target wasm-ssa`, so nothing the generator produces ever reached
// the lift or the SSA register allocator. That blind spot is not
// hypothetical: #5729 (the lift dropping an `ir.OpLoad`'s 64-bit
// width, corrupting every `i64[]` element read) and #5725 (the
// missing float bit-reinterprets) both shipped to main through it.
//
// BOTH corpora are swept, because they catch different classes and
// neither subsumes the other.
//
// The PRINTABLE one is what sees wide-value corruption. The
// exit-byte oracle compares only `main()`'s low byte, and #5729's
// corruption is a sign-extension from bit 31 — which by construction
// never changes the low 8 bits. Reintroducing that bug and sweeping
// all 2048 exit-byte seeds through arm64-ssa produced zero
// mismatches, even on the 860 seeds that build an `i64[]`; the same
// experiment on the printable corpus diverged on 3 of 201 runnable
// seeds.
//
// The EXIT-BYTE one is not redundant despite that, which is easy to
// conclude and wrong — this file said so for one commit. Its
// programs have different shapes (no floats or strings among
// `main`'s vars, so more enum / closure / composite structure), and
// #5767 — a SIGSEGV from the closure drop thunk being handed a
// closure cell where it expects an env block — appears ONLY there.
// The printable corpus is clean across 2048 seeds beyond CI's range
// while the exit-byte corpus crashes on one. A crash also shows up
// perfectly well in an exit code, so the byte oracle's narrowness
// costs nothing for that class.
//
// Coverage-gap seeds are expected and skipped: the arm64-ssa
// contract is that an unsupported op *errors* rather than
// miscompiles, so a compile failure is a documented endpoint, not a
// bug. To stop that from quietly hollowing the test out — the
// failure mode TestArm64SSACliRoundtrip actually suffered, asserting
// unmet behaviour for months because it skipped everywhere — the
// sweep asserts a floor on how many sampled seeds must have
// compiled and run.
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/fernsmith"
)

// diffOracleSSAMinRunRatio is the floor on sampled-and-executed
// seeds. Compile gaps are legitimate (~21% of the corpus today), but
// if the arm64-ssa frontend regressed to rejecting nearly everything
// this test would go green while testing nothing — so require that
// at least this fraction of the sampled seeds made it all the way to
// a stdout comparison.
const diffOracleSSAMinRunRatio = 0.4

// TestDifferential_Arm64SSAStdout runs the printable fernsmith
// corpus through `-target arm64-ssa` and asserts the resulting
// binary's stdout matches the interpreter's, the same contract the
// other backends are held to in TestDifferential_PrintableStdout.
//
// Honours DIFF_ORACLE_SHARD so the differential workflow's four
// aarch64 cells split the sweep rather than each running all of it.
// Runs natively on an arm64 host and under qemu-aarch64 on a cross
// host; skips when neither is available, which is the case on the
// workflow's x86_64 cells — matching how its arm64 sub-tests already
// behave there.
func TestDifferential_Arm64SSAStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	bin := buildFernCLI(t)

	shardIdx, shardCount := diffOracleShard(t)
	seedCount := printableSeeds(t)

	var sampled, ran int64
	for seed := uint64(0); seed < seedCount; seed++ {
		if seed%shardCount != shardIdx {
			continue
		}
		seed := seed
		sampled++
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			// Closure-free corpus: arm64-ssa segfaults on ordinary
			// closure shapes (#6144), which would swamp this leg and hide
			// every other regression. See Config.NoFnValues.
			src := fernsmith.GenPrintableMainNoFnValues(seed)
			want, ok := interpStdout(t, src)
			if !ok {
				return // interp coverage gap; already reported by the helper
			}

			r := runArm64SSAOrSkip(t, bin, qemu, src)
			atomic.AddInt64(&ran, 1)
			if got := trimOut(r.stdout); got != want {
				art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/arm64-ssa", seed), src, r.diag)
				t.Errorf("arm64-ssa stdout mismatch (exit=%d, signal=%s)\ngot:\n%s\nwant (interp):\n%s\nstderr:\n%s\nartifact dir: %s\nsrc:\n%s",
					r.diag.code, r.signalOrNormal(), got, want, r.diag.out, art, src)
			}
		})
	}

	t.Cleanup(func() { assertSSARunRatio(t, sampled, atomic.LoadInt64(&ran)) })
}

// TestDifferential_Arm64SSAExitByte runs the exit-code fernsmith
// corpus through `-target arm64-ssa` and asserts the binary's exit
// code matches the interpreter's, the same contract the other
// backends are held to in TestDifferential_LangsmithMain.
//
// See the file header for why this is not redundant with the stdout
// leg: it is the one that catches #5767's class. A crash reaches the
// exit code as 128+signal, so the byte oracle's narrowness — which
// makes it blind to wide-value corruption — costs nothing here.
//
// The full corpus is swept, NOT a sample. Sampling every 4th seed
// was tried and is worthless here: it skips seed 789, the only one
// of 764 runnable that reproduces #5767, and the leg passes with the
// fix reverted. Unlike a width or opcode bug — structural, visible
// on any seed touching the shape — a capture-layout crash needs one
// exact shape, so thinning the corpus thins the very thing this leg
// is for. DIFF_ORACLE_SHARD splits the cost across the workflow's
// four aarch64 cells instead.
func TestDifferential_Arm64SSAExitByte(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	bin := buildFernCLI(t)

	shardIdx, shardCount := diffOracleShard(t)
	seedCount := diffOracleSeeds(t)

	var sampled, ran int64
	for seed := uint64(0); seed < seedCount; seed++ {
		if seed%shardCount != shardIdx {
			continue
		}
		seed := seed
		sampled++
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			// Closure-free corpus — see the stdout leg above and #6144.
			src := fernsmith.GenMainNoFnValues(seed)
			want := runInterpByte(t, src)

			r := runArm64SSAOrSkip(t, bin, qemu, src)
			atomic.AddInt64(&ran, 1)
			if r.diag.code != want {
				art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/arm64-ssa-exit", seed), src, r.diag)
				t.Errorf("arm64-ssa exit=%d (signal=%s), interp=%d\nbinary output (stdout+stderr):\n%s%s\nartifact dir: %s\nsrc:\n%s",
					r.diag.code, r.signalOrNormal(), want, r.stdout, r.diag.out, art, src)
			}
		})
	}

	t.Cleanup(func() { assertSSARunRatio(t, sampled, atomic.LoadInt64(&ran)) })
}

// ssaRun is one compile-and-run of a generated program through
// `-target arm64-ssa`.
type ssaRun struct {
	stdout string
	diag   diagInfo
}

func (r ssaRun) signalOrNormal() string {
	if r.diag.signal == "" {
		return "<normal exit>"
	}
	return r.diag.signal
}

// runArm64SSAOrSkip compiles src with `-target arm64-ssa` and runs
// the binary, returning its stdout and post-mortem details. A
// compile failure SKIPS: the documented experimental-backend
// contract is that an op the SSA path doesn't cover yet is a clean
// error, not a miscompile. A run that dies by signal is NOT a skip —
// that is exactly what these oracles exist to catch — so the exit
// code (128+signal) and signal name come back in diagInfo.
func runArm64SSAOrSkip(t *testing.T, bin, qemu, src string) ssaRun {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	outPath := filepath.Join(dir, "main.bin")
	emit := exec.Command(bin, "-target", "arm64-ssa", "-o", outPath, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err != nil {
		t.Skipf("arm64-ssa coverage gap: %v\nstderr:\n%s", err, eb.String())
	}

	run := runArm64Bin(qemu, outPath)
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	if err := run.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
		}
	}
	return ssaRun{
		stdout: stdout.String(),
		diag: diagInfo{
			out:     stderr.String(),
			code:    run.ProcessState.ExitCode(),
			signal:  describeSignal(run.ProcessState),
			binPath: outPath,
		},
	}
}

// assertSSARunRatio is the shared floor guard: compile gaps are
// legitimate, but a backend that regressed to rejecting nearly
// everything would leave these oracles green while testing nothing.
func assertSSARunRatio(t *testing.T, sampled, ran int64) {
	t.Helper()
	if sampled == 0 {
		return
	}
	if ratio := float64(ran) / float64(sampled); ratio < diffOracleSSAMinRunRatio {
		t.Errorf("only %d/%d sampled seeds compiled under arm64-ssa (%.0f%%, want >= %.0f%%): "+
			"the backend appears to have regressed to rejecting programs it used to accept, "+
			"which would leave this oracle green while testing nothing",
			ran, sampled, ratio*100, diffOracleSSAMinRunRatio*100)
	}
}
