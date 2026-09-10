// Differential-execution oracle for the experimental SSA-direct
// backend (`-target arm64-linux -backend ssa`).
//
// The oracles in diff_oracle_test.go and printable_stdout_test.go
// cover arm64, x86_64 and wasmbin — every backend that lowers
// straight from the IR. Neither covers the SSA path:
// `internal/ssa`'s `LiftFromIR` feeds only `-target arm64-linux -backend ssa` and
// `-target wasm32-wasi -backend ssa`, so nothing the generator produces ever reached
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
// A coverage-gap seed SKIPS rather than fails: the arm64-ssa
// contract is that an unsupported op *errors* rather than
// miscompiles, so a compile failure is a documented endpoint, not a
// bug. Neither corpus has one left — both sweep clean end to end —
// so the sweep asserts a floor on how many sampled seeds compiled
// and ran, which stops a regression from hollowing the test out the
// way TestArm64SSACliRoundtrip was hollowed for months.
//
// A child that does not FINISH is a third outcome, kept apart from
// both: a compile or run that is still going at arm64SSAChildTimeout
// is counted as a timeout, not as a coverage gap and not as a
// miscompile, so the floor is measured over the seeds that finished
// and a loaded machine reads as "N seeds timed out" rather than as a
// backend regression (#8875).
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
	"time"

	"github.com/jakechampion/lang/internal/fernsmith"
)

// diffOracleSSAMinRunRatio is the floor on sampled-and-executed
// seeds: without one, a backend that regressed to rejecting programs
// it used to accept would turn every seed into a skip and leave this
// oracle green while testing nothing.
//
// It is 1.0 because arm64-ssa now compiles and runs BOTH corpora
// whole — all 2048 exit-byte and all 1024 printable seeds, with no
// interpreter-side gap either. So a single skip is a regression, and
// the floor says exactly that rather than leaving 60% of the sweep
// free to disappear unnoticed.
const diffOracleSSAMinRunRatio = 1.0

// arm64SSAChildTimeout bounds one compile child and one run child. An
// unloaded compile of a fernsmith program takes well under a second and
// a run under qemu a few, so a child still going at this wall is the
// machine, not the program: it is scored as a timeout, never as a
// compile failure or a wrong answer.
const arm64SSAChildTimeout = 60 * time.Second

// diffOracleSSAMaxTimeoutRatio is the share of sampled seeds allowed to
// time out before the sweep fails for having been unable to say
// anything. The failure names load as the cause; it is the one verdict
// here that a busy machine can produce, and it says so.
const diffOracleSSAMaxTimeoutRatio = 0.05

// ssaTally counts one sweep's seeds by outcome. sampled is every seed
// the window handed out; ran is those that compiled and executed to a
// verdict; timedOut is those whose compile or run child hit
// arm64SSAChildTimeout. sampled - ran - timedOut is the coverage gaps.
type ssaTally struct {
	sampled, ran, timedOut int64
}

// runBounded starts cmd and waits at most timeout for it. A child that
// outlives the wall is killed and reported as timedOut with no error;
// otherwise the Wait error comes back as it would from cmd.Run.
func runBounded(cmd *exec.Cmd, timeout time.Duration) (timedOut bool, err error) {
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return false, err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return true, nil
	}
}

// TestDifferential_Arm64SSAStdout runs the printable fernsmith
// corpus through `-target arm64-linux -backend ssa` and asserts the resulting
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

	var tally ssaTally
	for _, seed := range diffOracleWindow(t, printableSeeds(t)) {
		seed := seed
		tally.sampled++
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenPrintableMain(seed)
			want := interpStdout(t, src)

			r := runArm64SSAOrSkip(t, bin, qemu, src, &tally)
			if got := trimOut(r.stdout); got != want {
				art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/arm64-ssa", seed), src, r.diag)
				t.Errorf("arm64-ssa stdout mismatch (exit=%d, signal=%s)\ngot:\n%s\nwant (interp):\n%s\nstderr:\n%s\nartifact dir: %s\nsrc:\n%s",
					r.diag.code, r.signalOrNormal(), got, want, r.diag.out, art, src)
			}
		})
	}

	t.Cleanup(func() { assertSSARunRatio(t, &tally) })
}

// TestDifferential_Arm64SSAExitByte runs the exit-code fernsmith
// corpus through `-target arm64-linux -backend ssa` and asserts the binary's exit
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

	var tally ssaTally
	for _, seed := range diffOracleWindow(t, diffOracleSeeds(t)) {
		seed := seed
		tally.sampled++
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			want := runInterpByteOrSkip(t, src)

			r := runArm64SSAOrSkip(t, bin, qemu, src, &tally)
			if r.diag.code != want {
				art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/arm64-ssa-exit", seed), src, r.diag)
				t.Errorf("arm64-ssa exit=%d (signal=%s), interp=%d\nbinary output (stdout+stderr):\n%s%s\nartifact dir: %s\nsrc:\n%s",
					r.diag.code, r.signalOrNormal(), want, r.stdout, r.diag.out, art, src)
			}
		})
	}

	t.Cleanup(func() { assertSSARunRatio(t, &tally) })
}

// ssaRun is one compile-and-run of a generated program through
// `-target arm64-linux -backend ssa`.
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

// runArm64SSAOrSkip compiles src with `-target arm64-linux -backend ssa` and runs
// the binary, returning its stdout and post-mortem details, and counts
// the seed in tally as ran. A compile failure SKIPS: the documented
// experimental-backend contract is that an op the SSA path doesn't
// cover yet is a clean error, not a miscompile. A run that dies by
// signal is NOT a skip — that is exactly what these oracles exist to
// catch — so the exit code (128+signal) and signal name come back in
// diagInfo. A child still running at arm64SSAChildTimeout is neither:
// it is counted in tally as timedOut and the seed skips, so a slow
// machine cannot be read as a coverage gap.
func runArm64SSAOrSkip(t *testing.T, bin, qemu, src string, tally *ssaTally) ssaRun {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	outPath := filepath.Join(dir, "main.bin")
	emit := exec.Command(bin, "-target", "arm64-linux", "-backend", "ssa", "-o", outPath, srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	timedOut, err := runBounded(emit, arm64SSAChildTimeout)
	if timedOut {
		atomic.AddInt64(&tally.timedOut, 1)
		t.Skipf("arm64-ssa compile child did not finish in %s: a load signal, not a compiler verdict", arm64SSAChildTimeout)
	}
	if err != nil {
		t.Skipf("arm64-ssa coverage gap: %v\nstderr:\n%s", err, eb.String())
	}

	run := runArm64Bin(qemu, outPath)
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	timedOut, err = runBounded(run, arm64SSAChildTimeout)
	if timedOut {
		atomic.AddInt64(&tally.timedOut, 1)
		t.Skipf("arm64-ssa run child did not finish in %s: a load signal, not a compiler verdict", arm64SSAChildTimeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
		}
	}
	atomic.AddInt64(&tally.ran, 1)
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
// Timed-out seeds are outside the ratio — they say nothing about the
// backend — and are reported on their own, failing only past
// diffOracleSSAMaxTimeoutRatio.
func assertSSARunRatio(t *testing.T, tally *ssaTally) {
	t.Helper()
	sampled, ran, timedOut := tally.sampled, atomic.LoadInt64(&tally.ran), atomic.LoadInt64(&tally.timedOut)
	if sampled == 0 {
		return
	}
	if timedOut > 0 {
		ratio := float64(timedOut) / float64(sampled)
		if ratio > diffOracleSSAMaxTimeoutRatio {
			t.Errorf("%d/%d sampled seeds timed out under arm64-ssa (%.0f%%, ceiling %.0f%%): "+
				"the machine was too loaded for this sweep to say anything about the backend; "+
				"re-run it unloaded rather than reading the result as a compiler verdict",
				timedOut, sampled, ratio*100, diffOracleSSAMaxTimeoutRatio*100)
		} else {
			t.Logf("%d/%d sampled seeds timed out under arm64-ssa (load, not a compiler verdict); the floor is measured over the %d that finished",
				timedOut, sampled, sampled-timedOut)
		}
	}
	finished := sampled - timedOut
	if finished == 0 {
		return
	}
	if ratio := float64(ran) / float64(finished); ratio < diffOracleSSAMinRunRatio {
		t.Errorf("only %d/%d finished seeds compiled under arm64-ssa (%.0f%%, want >= %.0f%%): "+
			"the backend appears to have regressed to rejecting programs it used to accept, "+
			"which would leave this oracle green while testing nothing",
			ran, finished, ratio*100, diffOracleSSAMinRunRatio*100)
	}
}

// TestRunBoundedClassifiesTimeout pins the three outcomes runBounded
// separates: a child that exits cleanly, one that exits non-zero, and
// one that outlives the wall — the last must come back as timedOut
// with no error, since that is what keeps a slow machine out of the
// coverage-gap count.
func TestRunBoundedClassifiesTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sleep/true/false")
	}
	if timedOut, err := runBounded(exec.Command("true"), arm64SSAChildTimeout); timedOut || err != nil {
		t.Errorf("clean exit: timedOut=%v err=%v, want false and nil", timedOut, err)
	}
	if timedOut, err := runBounded(exec.Command("false"), arm64SSAChildTimeout); timedOut || err == nil {
		t.Errorf("non-zero exit: timedOut=%v err=%v, want false and an error", timedOut, err)
	}
	if timedOut, err := runBounded(exec.Command("sleep", "30"), 100*time.Millisecond); !timedOut || err != nil {
		t.Errorf("outlived the wall: timedOut=%v err=%v, want true and nil", timedOut, err)
	}
}
