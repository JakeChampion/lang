package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

const selfHostWasmDiffKnownFile = "selfhost-diff-wasm-known-divergences.txt"

const selfHostWasmDiffMinRunRatio = 0.45

func TestDifferential_SelfHostWasm(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	selfHostWasmSweep(t, gcc, selfHostWasmDiffKnownFile, nil, selfHostPassConfig)
}

func TestDifferential_SelfHostSanitizeWasm(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (x86-64 asm, argv paths)")
	}
	selfHostWasmSweep(t, gcc, selfHostWasmSanitizeKnownFile, []string{"-sanitize"}, nil)
}

const selfHostWasmSanitizeKnownFile = "selfhost-diff-sanitize-wasm-known-divergences.txt"

func selfHostWasmSweep(t *testing.T, gcc, knownFile string, flags []string, passes func(uint64) []string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the self-host wasm differential leg")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	known := loadKnownDivergences(t, knownFile)

	var sampled, compared, clamped int64
	for _, seed := range diffOracleWindow(t, selfHostDiffSeeds(t)) {
		seed := seed
		sampled++
		key := strconv.FormatUint(seed, 10)
		reason, isKnown := known[key]
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			src := fernsmith.GenMain(seed)
			want := runInterpByteOrSkip(t, src)

			diverged := false
			class := ""
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
			r, gap := runSelfHostWasmSeed(t, fernBin, stdlibRoot, src, flags, env, failf)
			bisect := func(class string) {
				report, pass := bisectSelfHostSeed(t, fernBin, stdlibRoot, "wasm32-wasi", src, flags, env, func(env []string) bool {
					r, gap := runSelfHostWasmSeed(t, fernBin, stdlibRoot, src, flags, env, func(string, ...any) {})
					return wasmRunAgrees(r, gap, want)
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
				if !isKnown {
					t.Skipf("self-host wasm coverage gap: %s", gap)
				}
				t.Errorf("seed %d is listed in testdata/%s (%s) but it no longer COMPILES, so the row "+
					"cannot be verified — re-check it and either update the reason or delete it:\n%s",
					seed, knownFile, reason, gap)
				return
			}
			if r == nil {
				return
			}
			defer func() {
				if class != "" {
					bisect(class)
				}
			}()
			out, exit := r.stdout+r.stderr, r.exit
			if rejected, why := wasmRejected(out); rejected {
				failf("wasmtime REJECTED the self-host module — not a wrong answer, the artifact is "+
					"invalid or incomplete (%s):\n%s\nsrc:\n%s", why, out, src)
				return
			}
			if strings.Contains(out, "wasm trap:") {
				failf("the self-host wasm module TRAPPED, which the interpreter's %d does not:\n%s\nsrc:\n%s",
					want, out, src)
				return
			}
			if strings.Contains(out, "fern-sanitizer:") {
				failf("the sanitizer reported on the self-host wasm module:\n%s\nsrc:\n%s", out, src)
				return
			}
			if want >= 126 {
				atomic.AddInt64(&clamped, 1)
				return
			}
			atomic.AddInt64(&compared, 1)
			if exit != want {
				failf("self-host wasm exit = %d, interpreter = %d\n%s\nsrc:\n%s", exit, want, out, src)
			}

			if isKnown && !diverged {
				t.Errorf("seed %d is listed in testdata/%s (%s) but it AGREES now — delete the entry",
					seed, knownFile, reason)
			}
		})
	}

	t.Cleanup(func() {
		got := atomic.LoadInt64(&compared)
		if sampled == 0 {
			return
		}
		t.Logf("compared %d of %d sampled seeds (%d uncomparable under the WASI exit clamp)",
			got, sampled, atomic.LoadInt64(&clamped))
		if ratio := float64(got) / float64(sampled); ratio < selfHostWasmDiffMinRunRatio {
			t.Errorf("only %d of %d sampled seeds were COMPARED (%.2f) — below the %.2f floor. "+
				"Compile gaps and the exit clamp are documented endpoints, but at this rate the leg is "+
				"not testing the compiler", got, sampled, ratio, selfHostWasmDiffMinRunRatio)
		}
	})
}

func wasmRunAgrees(r *selfHostRun, gap string, want int) bool {
	if r == nil || gap != "" {
		return false
	}
	out := r.stdout + r.stderr
	if rejected, _ := wasmRejected(out); rejected || strings.Contains(out, "wasm trap:") || strings.Contains(out, "fern-sanitizer:") {
		return false
	}
	return want >= 126 || r.exit == want
}

func runSelfHostWasmSeed(t *testing.T, fernBin, stdlibRoot, src string, flags, env []string, failf failFunc) (*selfHostRun, string) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	watPath := filepath.Join(dir, "prog.wat")
	emit := append(append([]string{}, flags...), "-emit", "asm")
	args := append([]string{"-target", "wasm32-wasi"}, emit...)
	args = append(args, "-o", watPath, srcPath, stdlibRoot)
	out, hung, err := compileSelfHostSeed(fernBin, env, args...)
	if hung {
		reportCompileHang(failf, src)
		return nil, ""
	}
	if err != nil {
		return nil, fmt.Sprintf("%v\n%s%s", err, out,
			strictIRBailSite(fernBin, "wasm32-wasi", emit, env, srcPath, stdlibRoot, out))
	}
	r := runSelfHostBin(exec.Command("wasmtime", "run", watPath), "")
	return &r, ""
}
