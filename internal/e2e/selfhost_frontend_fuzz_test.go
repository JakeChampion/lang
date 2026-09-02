package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostFuzzEnv gates the self-host front-end fuzzer.
//
// Its own switch rather than FERN_SELFHOST_DIFF: that one turns on a
// differential over GENERATED PROGRAMS, this one throws arbitrary TEXT at the
// front end, and the two want different budgets and lanes.
const selfHostFuzzEnv = "FERN_SELFHOST_FUZZ"

// FuzzSelfHostFrontEnd is the self-host answer to FuzzParse and FuzzCheck.
//
// Every other fuzz target in the repo drives the Go implementation (#7967).
// That asymmetry is not academic: the native parser's unbounded recursion was
// found by FuzzCheck and fixed in #7941, and the SAME defect sat in the
// self-host parser until #7979 — invisible, because nothing random ever
// reached it. This target closes that hole for the front end.
//
// # The property
//
// The self-host CLI must not CRASH or HANG on any input. Any exit code is
// fine, including a diagnostic: rejecting bad source is the job. Dying on a
// signal is not, and neither is spinning until the harness kills it.
//
// Deliberately not asserted: that the self-host and native agree on WHICH
// text is legal. Random bytes are almost all invalid, and the two front ends
// legitimately differ on how far recovery gets before giving up, so an
// agreement property here would be noise. Agreement is measured where it is
// meaningful — over generated programs that parse, in
// TestDifferential_SelfHostX86_64, and at the nesting bound in
// TestSelfHostDeepNestingMatchesNativeAndDoesNotCrash.
//
// # Why THIS target is not coverage-guided
//
// An iteration spawns a process and runs a whole compiler front end, so it
// costs milliseconds where an in-process target costs microseconds. And Go's
// coverage instrumentation cannot see inside a Fern binary, so the feedback
// that makes `-fuzz` steer is absent here — what mutates is the input, with
// nothing observing which self-host paths it reached. That makes this a fast
// random walk over arbitrary TEXT with a persistent corpus, which is still
// enough to find a segfault, and it is why the target is cheap enough to run
// alongside the Go front-end fuzzers.
//
// Coverage-guided self-host fuzzing now exists separately, built on #5548's
// `-cover`: TestSelfHostCoverageGuidedFuzz reads the counters an instrumented
// self-host binary dumps at exit and keeps the inputs that reach new ones.
// It steers GENERATED programs rather than arbitrary text, and costs ~500 ms
// an iteration, so it is a nightly lane rather than a `-fuzz` target.
//
// Run with:
//
//	FERN_SELFHOST_FUZZ=1 go test -run='^$' -fuzz=FuzzSelfHostFrontEnd ./internal/e2e
func FuzzSelfHostFrontEnd(f *testing.F) {
	if os.Getenv(selfHostFuzzEnv) == "" {
		f.Skip("set " + selfHostFuzzEnv + "=1 to fuzz the self-host front end")
	}
	gcc, runner := x86_64Tooling(f)
	if len(runner) != 0 {
		f.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		f.Fatalf("abs stdlib root: %v", err)
	}

	// Built ONCE. The driver build is minutes of wall clock and several GB;
	// per-iteration it would dominate so completely that the fuzzer would
	// execute a handful of inputs an hour.
	dir := writeSelfHostAsmProject(f)
	copySelfHostDriver(f, dir, "fern.fern")
	fernBin := buildSelfHostBin(f, gcc, dir, "fern.fern", "fern")

	// One scratch directory reused across iterations. A fuzz worker runs its
	// inputs one at a time, and a TempDir per input would leave a directory
	// per execution behind for the run's lifetime.
	work := f.TempDir()
	srcPath := filepath.Join(work, "main.fern")

	seeds := []string{
		``,
		`function main(): i32 { return 0; }`,
		`function main(): i32 { var a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + i; i = i + 1; } return s; }`,
		`struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; return p.x; }`,
		`enum E { A, B } function main(): i32 { match (E.A) { E.A => { return 0; }, E.B => { return 1; } } }`,
		// Already-invalid programs: the front end should diagnose, not die.
		`function main(): i32 { return true; }`,
		`function main(): i32 { return nope; }`,
		`function (`,
		// Nesting past the parser's bound — the shape that segfaulted the
		// self-host before #7979. Keeps a seed on the far side of the guard.
		"function main(): i32 { return " + strings.Repeat("(", 3000) + "0" + strings.Repeat(")", 3000) + "; }",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-check", srcPath)
		cmd.Env = append(os.Environ(), "FERN_STDLIB_ROOT="+stdlibRoot)
		r := runSelfHostBin(cmd, "")
		if !r.exited {
			t.Errorf("self-host front end died on a signal (%s) instead of reporting:\n%q\nstdout:\n%s\nstderr:\n%s",
				r.state, src, r.stdout, r.stderr)
		}
		if r.timedOut {
			t.Errorf("self-host front end hung on:\n%q", src)
		}
	})
}
