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
// This leg hangs off the PRINTABLE corpus, not the exit-byte one,
// and that choice is load-bearing. The exit-byte oracle compares
// only `main()`'s low byte, and the #5729 corruption is a
// sign-extension from bit 31 — which by construction never changes
// the low 8 bits. Reintroducing that bug and sweeping all 2048
// exit-byte seeds through arm64-ssa produces zero mismatches, even
// on the 860 seeds that build an `i64[]`. The same experiment on the
// printable corpus, where values are observed via `.to_string()` and
// the whole of stdout is compared, diverges on 3 of 201 runnable
// seeds. Wide-value corruption is only visible to an oracle that
// reads more than the bottom byte.
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
			src := fernsmith.GenPrintableMain(seed)
			want, ok := interpStdout(t, src)
			if !ok {
				return // interp coverage gap; already reported by the helper
			}

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
				// The documented experimental-backend contract: an op the
				// SSA path doesn't cover yet is a clean compile error.
				t.Skipf("arm64-ssa coverage gap: %v\nstderr:\n%s", err, eb.String())
			}
			atomic.AddInt64(&ran, 1)

			run := runArm64Bin(qemu, outPath)
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			if err := run.Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
				}
			}
			if got := trimOut(stdout.String()); got != want {
				d := diagInfo{
					out:     stderr.String(),
					code:    run.ProcessState.ExitCode(),
					signal:  describeSignal(run.ProcessState),
					binPath: outPath,
				}
				art := preserveDiagArtifacts(t, fmt.Sprintf("seed=%d/arm64-ssa", seed), src, d)
				sig := d.signal
				if sig == "" {
					sig = "<normal exit>"
				}
				t.Errorf("arm64-ssa stdout mismatch (exit=%d, signal=%s)\ngot:\n%s\nwant (interp):\n%s\nstderr:\n%s\nartifact dir: %s\nsrc:\n%s",
					d.code, sig, got, want, d.out, art, src)
			}
		})
	}

	t.Cleanup(func() {
		r := atomic.LoadInt64(&ran)
		if sampled == 0 {
			return
		}
		if ratio := float64(r) / float64(sampled); ratio < diffOracleSSAMinRunRatio {
			t.Errorf("only %d/%d sampled seeds compiled under arm64-ssa (%.0f%%, want >= %.0f%%): "+
				"the backend appears to have regressed to rejecting programs it used to accept, "+
				"which would leave this oracle green while testing nothing",
				r, sampled, ratio*100, diffOracleSSAMinRunRatio*100)
		}
	})
}
