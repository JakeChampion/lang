package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

const selfHostDiffMinRunRatio = 0.85

const selfHostDiffKnownFile = "selfhost-diff-x86_64-known-divergences.txt"

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
	if !envflag.On("FERN_SELFHOST_DIFF") {
		t.Skip("set FERN_SELFHOST_DIFF=1 to run the fernsmith corpus through the self-host compiler")
	}
}

func TestDifferential_SelfHostX86_64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	selfHostX86Sweep(t, gcc, selfHostDiffKnownFile, nil, selfHostPassConfig)
}

func TestDifferential_SelfHostSanitizeX86_64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	selfHostX86Sweep(t, gcc, selfHostSanitizeKnownFile, []string{"-sanitize"}, nil)
}

const selfHostSanitizeKnownFile = "selfhost-diff-sanitize-known-divergences.txt"

func selfHostX86Sweep(t *testing.T, gcc, knownFile string, flags []string, passes func(uint64) []string) {
	t.Helper()
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	known := loadKnownDivergences(t, knownFile)
	leg := diffLeg{fernBin: fernBin, stdlibRoot: stdlibRoot, target: "x86-64-linux", flags: flags,
		run: func(t *testing.T, src string, env []string) (*selfHostRun, string) {
			return runSelfHostSeed(t, fernBin, stdlibRoot, src, flags, env, func(string, ...any) {})
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
			r, gap := runSelfHostSeed(t, fernBin, stdlibRoot, src, flags, env, failf)
			triage := func(class string) { leg.triage(t, seed, src, want, env, class) }
			if gap != "" {
				if compilerRefused(gap) {
					t.Errorf("the compiler refused this seed:\n%s", gap)
					triage(refusalClass(gap))
					return
				}
				if !isKnown {
					t.Skipf("self-host coverage gap: %s", gap)
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
			t.Errorf("only %d of %d sampled seeds compiled, linked and ran (%.2f) — "+
				"below the %.2f floor. Compile gaps are a documented endpoint, but at "+
				"this rate the leg is not testing the compiler",
				got, sampled, ratio, selfHostDiffMinRunRatio)
		}
	})
}

var compilerRefusals = []string{
	"FERN_SEM_IR: the typed lowering refused",
	" emits unbalanced reference counting",
	" lowered to malformed IR",
}

func compilerRefused(out string) bool {
	for _, m := range compilerRefusals {
		if strings.Contains(out, m) {
			return true
		}
	}
	return false
}

var selfHostCompileTimeout = 2 * time.Minute

func compileSelfHostSeed(fernBin string, env []string, args ...string) (out []byte, hung bool, err error) {
	cmd := selfHostCompileCmd(fernBin, env, args...)
	var compiled bytes.Buffer
	cmd.Stdout, cmd.Stderr = &compiled, &compiled
	hung, err = runBounded(cmd, selfHostCompileTimeout)
	return compiled.Bytes(), hung, err
}

func reportCompileHang(failf failFunc, src string) {
	failf("the self-host compiler did not finish in %v:\nsrc:\n%s", selfHostCompileTimeout, src)
}

func runSelfHostSeed(t *testing.T, fernBin, stdlibRoot, src string, flags, env []string, failf failFunc) (*selfHostRun, string) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	args := append([]string{"-target", "x86-64-linux"}, flags...)
	args = append(args, "-emit", "asm", "-o", asmPath, srcPath, stdlibRoot)
	out, hung, err := compileSelfHostSeed(fernBin, env, args...)
	if hung {
		reportCompileHang(failf, src)
		return nil, ""
	}
	if err != nil {
		return nil, fmt.Sprintf("%v\n%s%s", err, out,
			strictIRBailSite(fernBin, "x86-64-linux", append(flags, "-emit", "asm"), env, srcPath, stdlibRoot, out))
	}
	binPath := filepath.Join(dir, "prog")
	if out, err := exec.Command("gcc", "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		failf("the assembler/linker REJECTED the self-host asm — the artifact never ran (%v):\n%s\nsrc:\n%s", err, out, src)
		return nil, ""
	}
	r := runSelfHostBin(exec.Command(binPath), "")
	return &r, ""
}

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
