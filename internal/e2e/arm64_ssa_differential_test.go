// Differential execution of the experimental `-target arm64-linux -backend ssa`
// backend against the shipping flat-IR arm64 backend, over the examples corpus.
//
// docs/SSA-DECISION.md states the proving-ground contract as "must stay
// byte-identical-in-behaviour to the flat-IR backends ... across their covered
// subset". Nothing enforced it. arm64_ssa_test.go is ~80 hand-written cases, so
// it only ever sees shapes somebody thought to write down, and diff_oracle_test.go
// samples the DEFAULT backends. Two defects shipped through that gap: a fixed
// 64 KiB .bss bump heap that SIGSEGVs the moment a program outgrows it, and an
// f32 stored into a multi-slot aggregate landing only in the last slot.
//
// # Three outcomes, not two
//
// Coverage is explicitly a documented subset, so "the SSA backend declined this
// program" is the flag working as specified, not a failure — and not a pass
// either. Each program lands in exactly one of:
//
//	baseline-rejected  the SHIPPING backend cannot build it, so there is no
//	                   reference answer and nothing to differentiate
//	ssa-refused        the SSA backend exited non-zero with a diagnostic: a
//	                   coverage gap, counted and logged
//	compared           both backends produced a binary, both ran
//
// and a compared program then agrees or diverges. A refusal that is really a
// compiler CRASH (killed by a signal) or a silent non-zero exit with no
// diagnostic is not a refusal — those fail, because "unsupported op errors
// rather than miscompiles" is the property the subset rests on.
//
// # Why exit code AND stdout
//
// Exit code alone would have missed one of the four wrong answers this leg
// reports on the tree it was written against: examples/wasm/shape_area.fern
// prints a wrong triangle area and still exits 0. stdout alone would miss a
// crash. Both observables, plus the signal/timeout status, are the answer.
//
// # Cost
//
// One `go build` of the CLI, then per program two in-process compiles (no
// external assembler or linker — arm64-linux links itself) and two runs. The
// binaries execute natively on an arm64 host and under qemu-aarch64 on a cross
// host; the whole sweep is a few minutes under emulation and far less native.
package e2e

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// arm64SSADiffKnownFile lists the corpus programs whose SSA-compiled
	// behaviour is KNOWN to disagree with the flat backend today, one
	// `<path> <reason>` row each. Exact in BOTH directions, as the self-host
	// legs' files are: a listed program that starts AGREEING fails too. An
	// allowlist nobody prunes is where bugs go to be forgotten, and this one
	// is mostly a single defect whose fix should empty it in one go.
	arm64SSADiffKnownFile = "arm64-ssa-diff-known-divergences.txt"

	// arm64SSADiffUnstableFile lists corpus programs whose STDOUT is not a
	// function of the compiler — a per-run temp path, a measured elapsed
	// time, or output that only stops when the harness kills the process.
	// Those are compared on exit status alone rather than dropped, so they
	// still count toward the floor and a crash under one backend is still
	// caught. A row here is not an allowlist for a wrong answer; a program
	// whose stdout genuinely differs between backends belongs in the
	// known-divergences file.
	arm64SSADiffUnstableFile = "arm64-ssa-diff-stdout-unstable.txt"

	// arm64SSADiffMinCorpus is the floor on the corpus WALK. A walk that
	// selects nothing passes with no sub-tests at all, which reads exactly
	// like a clean run (docs/TEST-GATES.md, practical rule 10) — and this one
	// selects by directory, so a corpus move empties it silently.
	arm64SSADiffMinCorpus = 250

	// arm64SSADiffMinCompared is the floor on programs that actually built
	// under BOTH backends and ran. This is the number the leg's value is
	// proportional to: a regression that widened the SSA bail set would
	// otherwise turn the lane green by testing almost nothing. Measured
	// 2026-08-22 over 281 corpus programs: 216 compared, 61 ssa-refused,
	// 4 baseline-rejected.
	arm64SSADiffMinCompared = 180

	// arm64SSADiffRunTimeout bounds one execution. The heaviest corpus
	// program (examples/bench/struct_drop.fern) takes ~0.9 s under
	// qemu-aarch64 on a 4-core container, so this is ~16x headroom.
	//
	// It is really sized against the programs that never terminate at all —
	// the listening servers under examples/wasm and examples/cli/yes.fern,
	// which reach it by design under both backends and so pay it twice. Those
	// dominate the lane: at 30 s they were two thirds of the whole run.
	arm64SSADiffRunTimeout = 15 * time.Second

	// arm64SSADiffMaxCapture caps per-stream capture. examples/cli/yes.fern
	// writes until it is killed, so an unbounded buffer would grow for the
	// whole timeout.
	arm64SSADiffMaxCapture = 1 << 20
)

// TestArm64SSABackendDifferential drives every examples/** program through both
// `-target arm64-linux` and `-target arm64-linux -backend ssa` and compares what
// the two binaries do.
//
// The name is `TestArm64*` deliberately: that prefix is what
// .github/workflows/test-e2e-arm64.yml selects, and that lane runs on
// ubuntu-24.04-arm where the emitted binaries execute natively, so the emulator
// branch below is unreachable there. On a cross host qemu-aarch64 is required;
// FERN_REQUIRE_ARM64_SSA_DIFF=1 turns a missing emulator into a failure, which
// is what the lane sets so that "the comparison ran" is checked rather than
// assumed.
func TestArm64SSABackendDifferential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64 backends are not exercised on windows")
	}
	runner := arm64SSADiffRunner(t)
	fern := buildFernCLI(t)
	corpus := arm64SSADiffCorpus(t)
	known := loadKnownDivergences(t, arm64SSADiffKnownFile)
	unstable := loadKnownDivergences(t, arm64SSADiffUnstableFile)
	arm64SSADiffCheckListedPaths(t, corpus, known, unstable)

	var baselineRejected, refused, agreed, diverged int64
	for _, rel := range corpus {
		rel := rel
		reason, isKnown := known[rel]
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			// A known row is a claim about the ANSWER this program produces,
			// so anything that stops it producing one makes the row
			// unverifiable. Saying so is what stops a row outliving whatever
			// it described.
			stale := func(what string, detail string) {
				t.Errorf("%s is listed in testdata/%s (%s) but it %s, so the row cannot be "+
					"verified — re-check it and either update the reason or delete it:\n%s",
					rel, arm64SSADiffKnownFile, reason, what, detail)
			}

			dir := t.TempDir()
			src := langSrcAbs(t, rel)

			baseBin := filepath.Join(dir, "base")
			if out, err := exec.Command(fern, "-target", "arm64-linux", "-o", baseBin, src).CombinedOutput(); err != nil {
				// No reference answer: the SHIPPING backend declined it (an
				// unavailable capability, a deliberately-invalid probe). Not a
				// finding about the SSA backend either way.
				atomic.AddInt64(&baselineRejected, 1)
				t.Logf("baseline-rejected: %v\n%s", err, firstLines(string(out), 3))
				if isKnown {
					stale("no longer BUILDS on the flat backend", firstLines(string(out), 3))
				}
				return
			}

			ssaBin := filepath.Join(dir, "ssa")
			cmd := exec.Command(fern, "-target", "arm64-linux", "-backend", "ssa", "-o", ssaBin, src)
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
				if isKnown {
					stale("no longer COMPILES under -backend ssa", firstLines(string(out), 3))
				}
				return
			}

			base := runArm64Binary(runner, baseBin)
			ssa := runArm64Binary(runner, ssaBin)
			_, stdoutUnstable := unstable[rel]
			if d := arm64SSADiffCompare(base, ssa, stdoutUnstable); d != "" {
				atomic.AddInt64(&diverged, 1)
				if isKnown {
					t.Logf("known divergence (%s): %s", reason, d)
					return
				}
				t.Errorf("`-backend ssa` DISAGREES with the flat arm64 backend on %s.\n%s\n"+
					"docs/SSA-DECISION.md holds the SSA backends to identical behaviour across "+
					"their covered subset; this program is inside the subset (it compiled) and "+
					"behaves differently, so it is a bug in the proving ground.\n"+
					"If the difference is not the compiler's — a per-run temp path, a measured "+
					"elapsed time — the program belongs in testdata/%s instead.",
					rel, d, arm64SSADiffUnstableFile)
				return
			}
			atomic.AddInt64(&agreed, 1)
			if isKnown {
				t.Errorf("%s is listed in testdata/%s (%s) but it AGREES now — delete the entry",
					rel, arm64SSADiffKnownFile, reason)
			}
		})
	}

	t.Cleanup(func() {
		a, r, d, b := atomic.LoadInt64(&agreed), atomic.LoadInt64(&refused),
			atomic.LoadInt64(&diverged), atomic.LoadInt64(&baselineRejected)
		compared := a + d
		t.Logf("arm64 flat-vs-ssa differential over %d corpus programs: %d agree, %d ssa-refused, "+
			"%d diverge (%d listed as known), %d baseline-rejected",
			len(corpus), a, r, d, len(known), b)
		if compared < arm64SSADiffMinCompared {
			t.Errorf("only %d of %d corpus programs built under BOTH backends and ran, below the %d "+
				"floor (%d ssa-refused, %d baseline-rejected). Refusals are a documented endpoint, "+
				"but at this rate the leg is not comparing the backends",
				compared, len(corpus), arm64SSADiffMinCompared, r, b)
		}
	})
}

// arm64SSADiffCompare returns "" when the two runs are indistinguishable, or a
// description of the first difference. The three failure modes are kept apart
// because they diagnose different things: a signal is a miscompile in the
// emitted code, a hang under one backend only is a lost loop condition, and a
// wrong exit or wrong stdout is a wrong answer.
//
// stdoutUnstable drops the stdout comparison for programs whose output is not a
// function of the compiler; every other observable still applies.
func arm64SSADiffCompare(base, ssa arm64Run, stdoutUnstable bool) string {
	switch {
	case base.timedOut != ssa.timedOut:
		hung, other := "ssa", base
		if base.timedOut {
			hung, other = "flat", ssa
		}
		return fmt.Sprintf("the %s build HUNG (killed at %s) while the other exited %d\n%s",
			hung, arm64SSADiffRunTimeout, other.exit, arm64SSADiffDetail(base, ssa))
	case base.timedOut && ssa.timedOut:
		return "" // Both run forever by design; nothing else is observable.
	case base.exited != ssa.exited:
		return fmt.Sprintf("one build CRASHED and the other did not — flat: %s, ssa: %s\n%s",
			base.state, ssa.state, arm64SSADiffDetail(base, ssa))
	case !base.exited && !ssa.exited && base.state != ssa.state:
		return fmt.Sprintf("both builds died, on different signals — flat: %s, ssa: %s\n%s",
			base.state, ssa.state, arm64SSADiffDetail(base, ssa))
	case base.exit != ssa.exit:
		return fmt.Sprintf("exit code: flat = %d, ssa = %d\n%s",
			base.exit, ssa.exit, arm64SSADiffDetail(base, ssa))
	case !stdoutUnstable && base.stdout != ssa.stdout:
		return fmt.Sprintf("stdout differs (both exited %d)\n%s\n%s",
			base.exit, firstStdoutDiff(base.stdout, ssa.stdout), arm64SSADiffDetail(base, ssa))
	}
	return ""
}

func arm64SSADiffDetail(base, ssa arm64Run) string {
	return fmt.Sprintf("  flat stdout: %s\n  flat stderr: %s\n   ssa stdout: %s\n   ssa stderr: %s",
		clip(base.stdout), clip(base.stderr), clip(ssa.stdout), clip(ssa.stderr))
}

// firstStdoutDiff names the first differing line, so a 200-line TAP stream
// reports the one case that changed rather than two walls of text.
func firstStdoutDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		x, y := "<eof>", "<eof>"
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("  first difference, line %d:\n    flat: %q\n     ssa: %q", i+1, x, y)
		}
	}
	return "  (no line differs; the streams differ only in trailing bytes)"
}

func clip(s string) string {
	const max = 600
	if len(s) > max {
		return fmt.Sprintf("%q… (%d bytes total)", s[:max], len(s))
	}
	return fmt.Sprintf("%q", s)
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… (%d more lines)", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

// arm64SSADiffRunner returns "" on a native arm64 Linux host (the emitted
// binaries execute directly) or the path to qemu-aarch64 on a cross host.
//
// A missing emulator is a skip with a reason, not a silent pass — but a skip is
// still what a lane that lost its toolchain looks like, and this file's
// neighbour arm64_ssa_test.go spent its whole existence asserting unmet
// behaviour that way. FERN_REQUIRE_ARM64_SSA_DIFF=1 makes the skip fatal, for
// any lane whose job is to establish that this comparison ran.
func arm64SSADiffRunner(t *testing.T) string {
	t.Helper()
	runner, ok := arm64Runner()
	if ok {
		return runner
	}
	if os.Getenv("FERN_REQUIRE_ARM64_SSA_DIFF") != "" {
		t.Fatal("FERN_REQUIRE_ARM64_SSA_DIFF is set but there is no way to run arm64 binaries here: " +
			"the host is not arm64 Linux and neither qemu-aarch64 nor qemu-aarch64-static is on PATH")
	}
	t.Skip("no way to run arm64 binaries: not an arm64 Linux host and no qemu-aarch64 on PATH " +
		"(set FERN_REQUIRE_ARM64_SSA_DIFF=1 to make that a failure)")
	return ""
}

// arm64SSADiffCorpus lists the examples corpus, repo-relative and slash-
// separated, in a stable order.
//
// examples/self_host is excluded: those files are the self-host compiler's own
// sources, which need argv and a stdlib root to do anything, take minutes each
// to build, and are gated by internal/e2eselfhost and the fixpoints. Everything
// else under examples/ is in — including the programs that turn out not to have
// a main, which build into something runnable anyway and so are perfectly good
// differential inputs.
func arm64SSADiffCorpus(t *testing.T) []string {
	t.Helper()
	root := langSrcAbs(t, "examples")
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "self_host" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".fern" {
			return nil
		}
		// Relative to the parent of examples/, so the key is the repo-relative
		// path the testdata files and langSrcAbs both speak.
		rel, relErr := filepath.Rel(filepath.Dir(root), path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	if len(out) < arm64SSADiffMinCorpus {
		t.Fatalf("the corpus walk under %s found %d .fern programs, below the %d floor — a walk that "+
			"selects nothing passes with no sub-tests at all, so this is a moved corpus rather than "+
			"a clean run", root, len(out), arm64SSADiffMinCorpus)
	}
	return out
}

// arm64SSADiffCheckListedPaths fails on a row naming a program the corpus does
// not contain. Without it a rename leaves a row that can never fire again, and
// the file reads as if it still covered something.
func arm64SSADiffCheckListedPaths(t *testing.T, corpus []string, lists ...map[string]string) {
	t.Helper()
	in := make(map[string]bool, len(corpus))
	for _, rel := range corpus {
		in[rel] = true
	}
	names := []string{arm64SSADiffKnownFile, arm64SSADiffUnstableFile}
	for i, list := range lists {
		for rel := range list {
			if !in[rel] {
				t.Errorf("testdata/%s lists %q, which is not in the corpus — delete the row or fix "+
					"the path", names[i], rel)
			}
		}
	}
}

// arm64Run is one execution of an emitted arm64 binary.
type arm64Run struct {
	stdout   string
	stderr   string
	exit     int
	exited   bool
	state    string
	timedOut bool
}

// runArm64Binary executes bin (directly, or under runner when cross-hosted)
// with an empty stdin, capping each captured stream and killing the process at
// arm64SSADiffRunTimeout. A program that only stops when it is killed is a
// legitimate corpus member here, so an unbounded buffer is not an option.
func runArm64Binary(runner, bin string) arm64Run {
	var cmd *exec.Cmd
	if runner == "" {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner, bin)
	}
	so, se := &cappedBuffer{limit: arm64SSADiffMaxCapture}, &cappedBuffer{limit: arm64SSADiffMaxCapture}
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = so, se
	if err := cmd.Start(); err != nil {
		return arm64Run{stderr: err.Error(), exit: -1, state: err.Error()}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	r := arm64Run{exit: -1}
	select {
	case <-done:
	case <-time.After(arm64SSADiffRunTimeout):
		r.timedOut = true
		_ = cmd.Process.Kill()
		<-done
	}
	r.stdout, r.stderr = so.String(), se.String()
	if ps := cmd.ProcessState; ps != nil {
		r.exit, r.exited, r.state = ps.ExitCode(), ps.Exited(), ps.String()
	}
	return r
}

// cappedBuffer keeps the first limit bytes written to it and drops the rest,
// noting how much was dropped.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
	total int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.total += len(p)
	if room := c.limit - c.buf.Len(); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		c.buf.Write(p[:room])
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	if c.total > c.buf.Len() {
		return fmt.Sprintf("%s\n…[%d of %d bytes dropped]", c.buf.String(), c.total-c.buf.Len(), c.total)
	}
	return c.buf.String()
}
