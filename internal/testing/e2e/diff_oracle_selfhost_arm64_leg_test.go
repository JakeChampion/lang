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

const selfHostArm64DiffKnownFile = "selfhost-diff-arm64-known-divergences.txt"

func TestDifferential_SelfHostArm64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	selfHostArm64Sweep(t, gcc, selfHostArm64DiffKnownFile, nil, selfHostPassConfig)
}

func TestDifferential_SelfHostSanitizeArm64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	selfHostArm64Sweep(t, gcc, selfHostArm64SanitizeKnownFile, []string{"-sanitize"}, nil)
}

const selfHostArm64SanitizeKnownFile = "selfhost-diff-sanitize-arm64-known-divergences.txt"

func selfHostArm64Sweep(t *testing.T, gcc, knownFile string, flags []string, passes func(uint64) []string) {
	t.Helper()
	_, qemu := arm64Tooling(t)

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	known := loadKnownDivergences(t, knownFile)
	leg := diffLeg{fernBin: fernBin, stdlibRoot: stdlibRoot, target: "arm64-linux", flags: flags,
		run: func(t *testing.T, src string, env []string) (*selfHostRun, string) {
			return runSelfHostArm64Seed(t, fernBin, stdlibRoot, qemu, src, flags, env, func(string, ...any) {})
		},
		agrees: seedRunAgrees}

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
			triage := func(class string) { leg.triage(t, seed, src, want, env, class) }
			if gap != "" {
				if compilerRefused(gap) {
					t.Errorf("the compiler refused this seed:\n%s", gap)
					triage(refusalClass(gap))
					return
				}
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
					triage(class)
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
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	r := runSelfHostBin(runArm64Bin(qemu, binPath), "")
	return &r, ""
}
