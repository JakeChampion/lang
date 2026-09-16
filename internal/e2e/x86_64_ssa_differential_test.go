package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// Differential execution of `-target x86-64-linux -backend ssa` against the
// shipping x86-64 backend, over the examples corpus.
//
// The arm64 sibling (arm64_ssa_differential_test.go) is the model, and its
// header explains why the shape is what it is. What that leg found on its FIRST
// run is the reason this one exists: four wrong answers and 56 SIGSEGVs. Nothing
// on x86-64 had ever been compared, and the helpers this backend runs are
// hand-written assembly — #8044 found five of them returning the rc header where
// the IR contract says they return their pointer, from compiling one enum
// program.
//
// Three outcomes, not two, exactly as the arm64 leg has them: the SSA coverage
// subset is documented and deliberate, so a refusal is the flag working, not a
// failure and not a pass either.
//
//	baseline-rejected  the SHIPPING backend cannot build it, so there is no
//	                   reference answer to differ from
//	ssa-refused        `-backend ssa` exited non-zero WITH a diagnostic: a
//	                   coverage gap, counted and logged
//	compared           both produced a binary, both ran, and both stopped —
//	                   by exiting, or by the wall (ssaDiffRunTimeout) on both
//
// A refusal that is really a compiler CRASH, or a silent non-zero exit with no
// diagnostic, is not a refusal and fails — "unsupported errors rather than
// miscompiles" is the property the whole subset rests on.
//
// Exit code AND stdout, because either alone misses a real class: a wrong answer
// that still exits 0, and a crash that prints nothing.
const (
	// x86SSADiffMinCorpus is the floor on the corpus WALK. A walk that selects
	// nothing passes with no sub-tests at all, which reads exactly like a clean
	// run (docs/TEST-GATES.md, practical rule 10).
	x86SSADiffMinCorpus = 250

	// x86SSADiffMinCompared is the floor on programs that built under BOTH
	// backends and ran — the number this leg's value is proportional to. A
	// regression that widened the SSA bail set would otherwise turn the lane
	// green by comparing almost nothing.
	//
	// 223 as measured 2026-09-16, once `args`, `env` and `stat` had
	// emitters: 194 came from #8570's `remove_dir_all` slice, 215 from the
	// four float reinterprets, the last refusal that was not a missing
	// runtime helper, 219 from the string builder — every program still
	// refused names one or more helpers with no emitter. RAISE IT with each
	// helper slice — that is the point of the number.
	//
	// The 105 still refused want the Map method family, the Reader/Writer
	// methods, and `__memcpy` / `__free`, in groups
	// (docs/SSA-CUTOVER-PLAN.md).
	x86SSADiffMinCompared = 223
)

func TestX86_64SSABackendDifferential(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("x86-64 binaries are run natively by this leg; have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	fern := buildFernCLI(t)
	corpus := arm64SSADiffCorpus(t) // the same walk; the corpus is not per-target

	var baselineRejected, refused, agreed, diverged, timedOut, tooSlow int64
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			src := langSrcAbs(t, rel)

			baseBin := filepath.Join(dir, "base")
			if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", baseBin, src).CombinedOutput(); err != nil {
				atomic.AddInt64(&baselineRejected, 1)
				t.Logf("baseline-rejected: %v\n%s", err, firstLines(string(out), 3))
				return
			}

			ssaBin := filepath.Join(dir, "ssa")
			cmd := exec.Command(fern, "-target", "x86-64-linux", "-backend", "ssa", "-o", ssaBin, src)
			out, err := cmd.CombinedOutput()
			if err != nil {
				if ps := cmd.ProcessState; ps != nil && !ps.Exited() {
					t.Errorf("the SSA compiler CRASHED on this program — %s (a coverage gap must be a "+
						"diagnostic, not a signal):\n%s", ps, firstLines(string(out), 10))
					return
				}
				if strings.TrimSpace(string(out)) == "" {
					t.Errorf("the SSA compiler exited %v with NO diagnostic — a refusal the reader "+
						"cannot act on is indistinguishable from a silent failure", err)
					return
				}
				atomic.AddInt64(&refused, 1)
				t.Logf("ssa-refused: %s", firstLines(string(out), 2))
				return
			}

			base := runSSADiffBinary("", baseBin)
			ssa := runSSADiffBinary("", ssaBin)
			// The wall expiring on one side only is a TIMEOUT, its own outcome:
			// it says nothing about the answer, and cannot tell a hang from a
			// build that is merely slower than the wall allows (#8069).
			if base.timedOut != ssa.timedOut {
				atomic.AddInt64(&timedOut, 1)
				late, other := "ssa", base
				if base.timedOut {
					late, other = "flat", ssa
				}
				t.Errorf("the %s build did not finish within %s on %s, while the other exited %d.\n"+
					"This is a TIMEOUT, not a disagreement — neither build has been shown to give\n"+
					"the wrong answer. Time the two directly before reading it as a hang.\n%s",
					late, ssaDiffRunTimeout, rel, other.exit, ssaDiffDetail(base, ssa))
				return
			}
			if d := ssaDiffCompare(base, ssa, false); d != "" {
				atomic.AddInt64(&diverged, 1)
				t.Errorf("`-backend ssa` DISAGREES with the shipping x86-64 backend on %s.\n%s\n"+
					"docs/SSA-DECISION.md holds the SSA backends to identical behaviour across "+
					"their covered subset, and this program is inside the subset because it compiled.",
					rel, d)
				return
			}
			atomic.AddInt64(&agreed, 1)
			// Same answer, wildly different cost (#8069). Re-measured once
			// before it is reported: the corpus runs in parallel on a shared
			// machine, and one slow sample is not a finding.
			if d := ssaSlowdown(base.elapsed, ssa.elapsed); d != "" {
				base2 := runSSADiffBinary("", baseBin)
				ssa2 := runSSADiffBinary("", ssaBin)
				if d2 := ssaSlowdown(base2.elapsed, ssa2.elapsed); d2 != "" {
					atomic.AddInt64(&tooSlow, 1)
					t.Errorf("%s: %s\nSecond measurement: %s", rel, d2, d)
				} else {
					t.Logf("%s: a slowdown that did not reproduce on a second run, so not reported: %s", rel, d)
				}
			}
		})
	}

	t.Cleanup(func() {
		a, r, d, b := atomic.LoadInt64(&agreed), atomic.LoadInt64(&refused),
			atomic.LoadInt64(&diverged), atomic.LoadInt64(&baselineRejected)
		compared := a + d
		t.Logf("x86-64 flat-vs-ssa differential over %d corpus programs: %d agree, %d ssa-refused, "+
			"%d diverge, %d did not finish under one backend, %d baseline-rejected, %d over the "+
			"%.0fx slowdown gate",
			len(corpus), a, r, d, atomic.LoadInt64(&timedOut), b, atomic.LoadInt64(&tooSlow),
			ssaDiffMaxSlowdown)
		if compared < x86SSADiffMinCompared {
			t.Errorf("only %d of %d corpus programs built under BOTH backends and ran, below the %d "+
				"floor (%d ssa-refused, %d baseline-rejected). Refusals are a documented endpoint, "+
				"but at this rate the leg is not comparing the backends",
				compared, len(corpus), x86SSADiffMinCompared, r, b)
		}
	})
}
