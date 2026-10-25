package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

const selfHostEMISteps = 8

func selfHostEMISeeds(t *testing.T) uint64 {
	t.Helper()
	if v := os.Getenv("SELFHOST_EMI_SEEDS"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("SELFHOST_EMI_SEEDS=%q: %v", v, err)
		}
		return n
	}
	if testing.Short() {
		return 64
	}
	return 256
}

// emiCases are the conformance cases a mutant can stand in for: one module,
// run on the interpreter and on x86-64, and not a known divergence there.
func emiCases(t *testing.T) []*fixtureSpec {
	t.Helper()
	known := loadKnownDivergences(t, "selfhost-x86_64-known-divergences.txt")
	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceCases, err)
	}
	var out []*fixtureSpec
	for _, e := range entries {
		dir, err := filepath.Abs(filepath.Join(conformanceCases, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		mods, _ := filepath.Glob(filepath.Join(dir, "*.fern"))
		if !e.IsDir() || len(mods) != 1 || filepath.Base(mods[0]) != "main.fern" {
			continue
		}
		if _, isKnown := known[e.Name()]; isKnown {
			continue
		}
		f := loadFixture(t, dir)
		if !f.rejectionCase() && f.backends["interp"] && f.backends["x86_64"] && !f.reclaimObservable {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// TestDifferential_SelfHostEMIX86_64 compiles mutants of the conformance cases
// with the self-host compiler (fernsmith.EMI). A mutant must print and return
// what its case expects, on the interpreter first and then on x86-64.
func TestDifferential_SelfHostEMIX86_64(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	e2eharness.BuildLangBinForInterp(t)

	cases := emiCases(t)
	if len(cases) == 0 {
		t.Fatal("no conformance case can stand in for an EMI mutant")
	}
	for _, seed := range diffOracleWindow(t, selfHostEMISeeds(t)) {
		seed, f := seed, cases[seed%uint64(len(cases))]
		t.Run(fmt.Sprintf("seed=%d/%s", seed, f.name), func(t *testing.T) {
			t.Parallel()
			orig, err := os.ReadFile(f.mainPath)
			if err != nil {
				t.Fatal(err)
			}
			src, kept := fernsmith.EMI(string(orig), seed, selfHostEMISteps, checksClean)
			if kept == 0 {
				t.Skip("no EMI step type-checks in this case")
			}
			m := *f
			m.mainPath = filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(m.mainPath, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			failed, class := false, ""
			failf := func(format string, args ...any) {
				if !failed {
					class = failureClass(format)
				}
				failed = true
				t.Errorf(format, args...)
			}
			out, exit := e2eharness.RunFixtureInterp(t, m.mainPath, m.stdin)
			checkSelfHostNativeRun(t, &m, selfHostRun{stdout: out, exit: exit, exited: true}, func(format string, args ...any) {
				failf("the interpreter: "+format, args...)
			})
			if failed {
				t.Logf("mutant of %s (%d steps):\n%s", f.name, kept, src)
				return
			}
			binPath := filepath.Join(t.TempDir(), "prog")
			cmd := fixtureCompile(fernBin, "-target", "x86-64-linux", "-o", binPath, m.mainPath, stdlibRoot)
			cmd.Env = append([]string{e2eharness.SelfHostVerify}, os.Environ()...)
			if out, err := cmd.CombinedOutput(); err != nil {
				failf("self-host compile failed: %v\n%s", err, out)
			} else {
				checkSelfHostNativeRun(t, &m, runSelfHostBin(exec.Command(binPath), m.stdin), failf)
			}
			if failed {
				t.Logf("mutant of %s (%d steps):\n%s", f.name, kept, src)
				recordFinding(t, selfHostFinding{Test: legName(t), Seed: seed, Class: class})
			}
		})
	}
}
