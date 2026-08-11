// Differential-execution oracle for the SELF-HOST compiler.
//
// Every other oracle in this package drives the NATIVE compiler:
// diff_oracle_test.go builds each generated program with x86_64.Emit /
// arm64.Emit / wasmbin.Build, diff_oracle_ssa_test.go with the SSA
// backends. The self-host compiler — the one that is meant to become
// the product — was in none of those chains, so a green differential
// run said the native backends agree and said nothing at all about
// self-host lowering (#6138).
//
// That gap had a specific cost. The closure-dispatch cluster
// docs/TEST-GATES.md names (#5001 / #5007 / #5009 / #5026 — escaping closures and closure
// arrays that lowered on the self-host IR path and then SIGSEGV'd or
// silently miscompiled) was found by hand-written differential probes,
// one program at a time, because nothing swept a corpus down that path.
// #6073 then taught fernsmith to emit exactly those shapes — closures
// in 43% of the corpus, escaping ones in 6% — and fed them to oracles
// that could not see the path the cluster was on. This leg closes that
// loop: the shape is generated AND the comparison that catches it runs.
//
// It found real miscompiles on its first run and continues to; see the
// known-divergences file for the current list.
//
// # Why exit code rather than stdout
//
// The byte oracle is the one that sees crashes, and crashes are what
// this class of bug produces: a SIGSEGV arrives as 139, an arena
// exhaustion as 125, and both are distinguishable here from a merely
// wrong answer. A native binary propagates main's return over 0..255,
// so unlike a wasm leg there is no WASI [0..126) cap to work around.
//
// # Cost, and why it is opt-in
//
// Building the self-host CLI is a heavy driver build (~4.3 GB reserved
// by the harness's memory limiter, minutes of wall clock). It is paid
// ONCE and amortised over the corpus; each seed is then a ~1.3s compile
// plus a link and a run. That is still far more than the native oracle's
// per-seed cost, so this is gated on FERN_SELFHOST_DIFF=1 and sharded in
// CI rather than slowing every local `go test ./internal/e2e`.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/fernsmith"
)

// selfHostDiffMinRunRatio is the floor on sampled-and-executed seeds.
//
// Compile gaps are a legitimate endpoint — the self-host backends route
// IR-or-error, so a construct outside the IR subset is a diagnostic
// naming the bail site rather than a miscompile — but they are the
// MAJORITY of this corpus today (measured 2026-08-05: 51% of seeds bail
// with "module is not IR-eligible"). That makes a floor essential rather
// than decorative: without one, a regression that widened the bail set
// would turn this lane green by testing almost nothing, and the shape of
// the corpus means nobody would notice the count moving.
//
// Set below the measured runnable fraction with room for the natural
// drift a generator change causes, and well above "the leg has hollowed
// out".
const selfHostDiffMinRunRatio = 0.3

// selfHostDiffKnownFile lists the seeds whose self-host result is KNOWN
// to disagree with the interpreter today, one per line, each against its
// tracking issue. Same contract as the fixture legs' files: a listed seed
// that starts PASSING fails too, because an allowlist nobody prunes is
// where bugs go to be forgotten.
const selfHostDiffKnownFile = "selfhost-diff-x86_64-known-divergences.txt"

// selfHostDiffSeeds is this leg's corpus size — its own knob rather than the
// native oracle's diffOracleSeeds, because a seed costs ~20x more here: a
// ~1.3s self-host compile plus a link and a run, against a fraction of a
// second for an in-process native Emit. Sweeping the native oracle's 2048
// takes hours serially, which is a lane nobody would keep.
//
// 512 is where the cost lands in the same range as the other differential
// lanes once CI's four shards split it, and it is far past the point where
// the corpus stops finding new SHAPES — measured 2026-08-05, seeds 0..299
// already produce every divergence class the leg reports. Raise it with
// SELFHOST_DIFF_SEEDS when hunting; the known-divergences file only covers
// the default range, so a larger sweep is expected to surface unlisted rows
// and should be run by hand rather than in the lane.
func selfHostDiffSeeds(t *testing.T) uint64 {
	t.Helper()
	if v := os.Getenv("SELFHOST_DIFF_SEEDS"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("SELFHOST_DIFF_SEEDS=%q: %v", v, err)
		}
		return n
	}
	if testing.Short() {
		return 64
	}
	return 512
}

func requireSelfHostDiffLeg(t *testing.T) {
	t.Helper()
	if os.Getenv("FERN_SELFHOST_DIFF") == "" {
		t.Skip("set FERN_SELFHOST_DIFF=1 to run the fernsmith corpus through the self-host compiler")
	}
}

// TestDifferential_SelfHostX86_64 compiles each generated program with the
// self-host CLI (`fern.fern -target x86-64-linux`), links the emitted GAS text with
// gcc, runs it, and asserts the exit byte matches the interpreter's.
//
// The CLI driver rather than asm_ir_run, for the reason the fixture legs give:
// it LOADS MODULES. A driver without a loader silently ignores an unresolved
// import and then reports a broken program's verdict
// (parser.warn_unresolved_imports, #6004).
//
// gcc rather than an in-process assembler because the self-host x86-64 path has
// none — it stops at asm text. That puts the external assembler on the critical
// path, so asm the assembler REJECTS surfaces here as a link failure rather than
// hiding, which is the #5862 / #6022 class.
func TestDifferential_SelfHostX86_64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The driver takes host filesystem paths as argv, so a qemu runner
		// would not see the same paths. Native-only, like every other
		// fern.fern test (see TestSelfHostCLIX86_64).
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	shardIdx, shardCount := diffOracleShard(t)
	seedCount := selfHostDiffSeeds(t)
	known := loadKnownDivergences(t, selfHostDiffKnownFile)

	var sampled, ran int64
	for seed := uint64(0); seed < seedCount; seed++ {
		if seed%shardCount != shardIdx {
			continue
		}
		seed := seed
		sampled++
		key := strconv.FormatUint(seed, 10)
		reason, isKnown := known[key]
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			want := runInterpByte(t, src)

			diverged := false
			failf := func(format string, args ...any) {
				if isKnown {
					diverged = true
					t.Logf("known divergence (%s): "+format, append([]any{reason}, args...)...)
					return
				}
				t.Errorf(format, args...)
			}
			r, gap := runSelfHostSeed(t, fernBin, stdlibRoot, src, failf)
			if gap != "" {
				// A compile bail is a documented endpoint, so an unlisted seed
				// SKIPS. A listed one does not: the row says this seed produces
				// a wrong ANSWER, and a seed that no longer compiles cannot
				// demonstrate that. Skipping there would let the row outlive
				// whatever it described, unverified, which is the exact way an
				// allowlist rots — so say the row is stale and make someone look.
				if !isKnown {
					t.Skipf("self-host coverage gap: %s", gap)
				}
				t.Errorf("seed %d is listed in testdata/%s (%s) but it no longer COMPILES, so the row "+
					"cannot be verified — re-check it and either update the reason or delete it:\n%s",
					seed, selfHostDiffKnownFile, reason, gap)
				return
			}
			if r != nil {
				atomic.AddInt64(&ran, 1)
				checkSelfHostSeedExit(*r, want, src, failf)
			}
			if isKnown && !diverged {
				t.Errorf("seed %d is listed in testdata/%s (%s) but it AGREES now — delete the entry",
					seed, selfHostDiffKnownFile, reason)
			}
		})
	}

	t.Cleanup(func() {
		got := atomic.LoadInt64(&ran)
		if sampled == 0 {
			return
		}
		if ratio := float64(got) / float64(sampled); ratio < selfHostDiffMinRunRatio {
			t.Errorf("only %d of %d sampled seeds compiled, linked and ran (%.2f) — "+
				"below the %.2f floor. Compile gaps are a documented endpoint, but at "+
				"this rate the leg is not testing the compiler",
				got, sampled, ratio, selfHostDiffMinRunRatio)
		}
	})
}

// runSelfHostSeed compiles src with the self-host CLI, links it, and runs it.
//
// Returns (run, "") when the program ran, (nil, gapMessage) when the compiler
// bailed, and (nil, "") when the LINK failed — a link failure has already been
// reported through failf, because the compiler emitted asm the assembler
// rejected and the artifact never ran, which is a finding this leg exists to
// report rather than a coverage gap to skip.
//
// It reports the bail rather than calling t.Skip itself. A skip unwinds the
// subtest goroutine immediately, which silently bypassed the caller's
// "listed but agrees now" check — so a listed seed that stopped compiling kept
// its row forever with nothing left to verify it. Returning the message leaves
// that decision where the known-list is in scope.
//
// Runs through runSelfHostBin so a diverging binary that HANGS is killed at
// selfHostRunTimeout and reported as such, rather than spending the lane's
// budget. That matters more here than on the fixture legs: these programs are
// generated, so nobody has ever eyeballed one to know it should terminate.
func runSelfHostSeed(t *testing.T, fernBin, stdlibRoot, src string, failf failFunc) (*selfHostRun, string) {
	t.Helper()
	// Absolute paths throughout: a relative one was unopenable from an
	// arm64-darwin binary until #6002, and absolute is what every other
	// self-host harness uses anyway.
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	out, err := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", srcPath, stdlibRoot, "-o", asmPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Sprintf("%v\n%s%s", err, out,
			strictIRBailSite(fernBin, "x86-64-linux", []string{"-emit", "asm"}, srcPath, stdlibRoot, out))
	}
	binPath := filepath.Join(dir, "prog")
	// The flags every other self-host x86 link uses (linkSelfHostAsm's small
	// path).
	if out, err := exec.Command("gcc", "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		failf("the assembler/linker REJECTED the self-host asm — the artifact never ran (%v):\n%s\nsrc:\n%s", err, out, src)
		return nil, ""
	}
	r := runSelfHostBin(exec.Command(binPath), "")
	return &r, ""
}

// checkSelfHostSeedExit is checkSelfHostNativeRun's oracle-side sibling: the
// same three-way split (hang / signal / wrong number), against the
// interpreter's byte rather than a fixture's declared exit. Kept apart from a
// plain equality check because the three mean different things — a signal is a
// miscompile in the emitted code, 125 is the arena, a wrong number is a wrong
// answer — and collapsing them is what makes a differential failure take an
// afternoon to triage.
func checkSelfHostSeedExit(r selfHostRun, want int, src string, failf failFunc) {
	switch {
	case r.timedOut:
		failf("the self-host-compiled binary HUNG — killed at %s\nstdout so far: %q\nstderr: %s\nsrc:\n%s",
			selfHostRunTimeout, r.stdout, r.stderr, src)
	case !r.exited:
		failf("the self-host-compiled binary CRASHED after %s — %s (not a wrong answer); interp exits %d\nstdout: %q\nstderr: %s\nsrc:\n%s",
			r.elapsed.Round(time.Millisecond), r.state, want, r.stdout, r.stderr, src)
	case r.exit != want:
		hint := ""
		if r.exit == 125 {
			hint = " — 125 is __fern_alloc's arena-exhaustion abort, so this is a leak rather than a wrong answer"
		}
		failf("self-host exit = %d, interp = %d%s\nstdout: %q\nstderr: %s\nsrc:\n%s",
			r.exit, want, hint, r.stdout, r.stderr, src)
	}
}
