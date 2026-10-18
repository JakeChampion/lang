package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

// selfHostArm64DiffKnownFile lists the seeds whose self-host ARM64 result is
// known to disagree with the interpreter, same contract as the x86-64 leg's:
// a listed seed that starts passing fails too.
const selfHostArm64DiffKnownFile = "selfhost-diff-arm64-known-divergences.txt"

// TestDifferential_SelfHostArm64 is the x86-64 oracle's sibling against the
// self-host ARM64 backend (#7967).
//
// Until this, generated programs only ever reached the self-host through its
// x86-64 emitter. The arm64 emitter had fixture coverage
// (TestFernFixturesSelfHostArm64) and nothing random at all, so a wrong answer
// it produced on a shape no fixture happens to contain had nowhere to surface.
//
// # Why this still runs on an x86-64 host
//
// The self-host DRIVER is emitted as x86-64 asm whatever it is asked to
// target, so it only executes on an x86-64 host — an arm64 runner cannot run
// the compiler at all. What varies is the artifact: the driver runs natively,
// emits an arm64 binary, and that binary runs under qemu-aarch64. Same shape
// as the arm64 fixture leg.
//
// # And why there is no gcc here
//
// `-target arm64-linux` assembles and links IN-PROCESS (arm64_native +
// elf.fern), so the self-host produces the finished binary itself. That makes
// this the only differential leg testing the self-hosted toolchain end to end,
// and it means an in-process-assembler gap arrives as a compile failure naming
// the mnemonic rather than as a link error.
func TestDifferential_SelfHostArm64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// Cannot exec the x86-64 driver, so there is no compiler to run.
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	selfHostArm64Sweep(t, gcc, selfHostArm64DiffKnownFile, nil, selfHostPassConfig)
}

// TestDifferential_SelfHostSanitizeArm64 is the arm64 sweep with every seed
// built under -sanitize, TestDifferential_SelfHostSanitizeX86_64's sibling:
// the rc ops and the over-release and freed-block checks are emitted per
// backend (asm_arm64_ir.fern has its own), so the x86-64 leg says nothing
// about this one's.
func TestDifferential_SelfHostSanitizeArm64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	selfHostArm64Sweep(t, gcc, selfHostArm64SanitizeKnownFile, []string{"-sanitize"}, nil)
}

// selfHostArm64SanitizeKnownFile is TestDifferential_SelfHostSanitizeArm64's
// known-divergences file, on the same contract.
const selfHostArm64SanitizeKnownFile = "selfhost-diff-sanitize-arm64-known-divergences.txt"

// selfHostArm64Sweep runs the fernsmith window through the self-host arm64
// CLI with the extra compile flags given, against the interpreter. A
// -sanitize build is also failed on any sanitizer report. A non-nil passes
// names the compiler switches each seed is compiled under.
func selfHostArm64Sweep(t *testing.T, gcc, knownFile string, flags []string, passes func(uint64) []string) {
	t.Helper()
	// Reused for its qemu discovery and clean skip; the gcc it returns is
	// unused because the target assembles and links in-process.
	_, qemu := arm64Tooling(t)

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	known := loadKnownDivergences(t, knownFile)

	var sampled, ran int64
	for _, seed := range diffOracleWindow(t, selfHostDiffSeeds(t)) {
		seed := seed
		sampled++
		key := strconv.FormatUint(seed, 10)
		reason, isKnown := known[key]
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			want := runInterpByteOrSkip(t, src)

			diverged, class := false, ""
			failf := func(format string, args ...any) {
				if isKnown {
					diverged = true
					t.Logf("known divergence (%s): "+format, append([]any{reason}, args...)...)
					return
				}
				if class == "" {
					class = failureClass(format)
				}
				t.Errorf(format, args...)
			}

			env := passEnv(passes, seed)
			logPassConfig(t, env)
			r, gap := runSelfHostArm64Seed(t, fernBin, stdlibRoot, qemu, src, flags, env, failf)
			bisect := func(class string) {
				report, pass := bisectSelfHostSeed(t, fernBin, stdlibRoot, "arm64-linux", src, flags, env, func(env []string) bool {
					r, gap := runSelfHostArm64Seed(t, fernBin, stdlibRoot, qemu, src, flags, env, func(string, ...any) {})
					return seedRunAgrees(r, gap, want, src)
				})
				t.Log(report)
				recordFinding(t, selfHostFinding{Test: legName(t), Seed: seed, Class: class, Pass: pass})
			}
			if gap != "" {
				if compilerRefused(gap) {
					t.Errorf("the compiler refused this seed:\n%s", gap)
					bisect(refusalClass(gap))
					return
				}
				// Same contract as the x86-64 leg: a compile bail is a
				// documented endpoint for an unlisted seed, but a LISTED one
				// that no longer compiles cannot demonstrate the wrong answer
				// its row claims, so the row is stale and someone must look.
				if !isKnown {
					t.Skipf("self-host arm64 coverage gap: %s", gap)
				}
				t.Errorf("seed %d is listed in testdata/%s (%s) but it no longer COMPILES, so the row "+
					"cannot be verified — re-check it and either update the reason or delete it:\n%s",
					seed, knownFile, reason, gap)
				return
			}
			if r != nil {
				atomic.AddInt64(&ran, 1)
				checkSelfHostSeedExit(*r, want, src, failf)
				if strings.Contains(r.stderr, "fern-sanitizer:") {
					failf("the sanitizer reported on the self-host-compiled binary:\n%s\nsrc:\n%s", r.stderr, src)
				}
				if class != "" {
					bisect(class)
				}
			}
			if isKnown && !diverged {
				t.Errorf("seed %d is listed in testdata/%s (%s) but it AGREES now — delete the entry",
					seed, knownFile, reason)
			}
		})
	}

	t.Cleanup(func() {
		got := atomic.LoadInt64(&ran)
		if sampled == 0 {
			return
		}
		if ratio := float64(got) / float64(sampled); ratio < selfHostDiffMinRunRatio {
			t.Errorf("only %d of %d sampled seeds compiled and ran (%.2f) — below the %.2f floor. "+
				"Compile gaps are a documented endpoint, but at this rate the leg is not testing the compiler",
				got, sampled, ratio, selfHostDiffMinRunRatio)
		}
	})
}

// runSelfHostArm64Seed compiles src for arm64-linux with the self-host CLI and
// runs the binary it produces. Returns (run, "") when it ran, (nil, gap)
// when the compiler bailed, and (nil, "") when the compile hung, which is
// already reported through failf.
//
// No link step and so no link failure to report: the self-host assembles and
// links this target itself, which folds what would be the x86-64 leg's link
// error into the compile failure, naming the unsupported mnemonic.
func runSelfHostArm64Seed(t *testing.T, fernBin, stdlibRoot, qemu, src string, flags, env []string, failf failFunc) (*selfHostRun, string) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	binPath := filepath.Join(dir, "prog")
	args := append([]string{"-target", "arm64-linux"}, flags...)
	args = append(args, "-o", binPath, srcPath, stdlibRoot)
	out, hung, err := compileSelfHostSeed(fernBin, env, args...)
	if hung {
		reportCompileHang(failf, src)
		return nil, ""
	}
	if err != nil {
		return nil, fmt.Sprintf("%v\n%s%s", err, out,
			strictIRBailSite(fernBin, "arm64-linux", flags, env, srcPath, stdlibRoot, out))
	}
	// write_file does not set the exec bit (the Makefile chmods
	// bin/fern-selfhost for the same reason).
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	r := runSelfHostBin(runArm64Bin(qemu, binPath), "")
	return &r, ""
}
