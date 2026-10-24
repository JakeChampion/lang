package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var corpusRegistry = map[string]func(*testing.T) []invocation{}

func registerCorpus(util string, cases func(*testing.T) []invocation) {
	if _, dup := corpusRegistry[util]; dup {
		panic("coreutils: registerCorpus called twice for " + util)
	}
	corpusRegistry[util] = cases
}

func utilNames(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "coreutils", "*.fern"))
	if err != nil {
		t.Fatalf("glob coreutils: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no coreutils/*.fern found — the gate is looking in the wrong place")
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".fern"))
	}
	return names
}

func TestSelfHostCoreutilsCoverage(t *testing.T) {
	for _, util := range utilNames(t) {
		if _, ok := corpusRegistry[util]; !ok {
			t.Errorf("coreutils/%s.fern is not in the corpus registry — every utility is compiled and run both ways, so add registerCorpus(%q, ...) to internal/testing/coreutils/%s_test.go", util, util, util)
		}
	}
}

var (
	selfHostOnce sync.Once
	selfHostPath string
	selfHostFail string

	selfHostBinsMu sync.Mutex
	selfHostBins   = map[string]string{}
	selfHostBinDir string
)

func selfHostCompiler(t *testing.T) string {
	t.Helper()
	selfHostOnce.Do(func() {
		fern := e2eharness.BuildLangBinForInterp(t)
		dir, err := os.MkdirTemp("", "fern-coreutils-selfhost-")
		if err != nil {
			selfHostFail = err.Error()
			return
		}
		bin := filepath.Join(dir, "fern-selfhost")
		selfHostDir := filepath.Join(repoRoot(t), "compiler")
		e2eharness.TrackFernSources(t, selfHostDir, "fern.fern")
		src := filepath.Join(selfHostDir, "fern.fern")
		out, cerr := exec.Command(fern, "-target", fernTarget(t), "-o", bin, src).CombinedOutput()
		if cerr != nil {
			selfHostFail = string(out)
			return
		}
		selfHostPath = bin
	})
	if selfHostPath == "" {
		t.Fatalf("build the self-host compiler: %s", selfHostFail)
	}
	return selfHostPath
}

func selfHostBin(t *testing.T, util string) string {
	t.Helper()
	selfHostBinsMu.Lock()
	defer selfHostBinsMu.Unlock()
	if bin, ok := selfHostBins[util]; ok {
		return bin
	}
	if selfHostBinDir == "" {
		dir, err := os.MkdirTemp("", "fern-coreutils-selfhost-bin-")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
		selfHostBinDir = dir
	}
	root := repoRoot(t)
	bin := filepath.Join(selfHostBinDir, util)
	argv := crossArgv(selfHostCompiler(t), "-target", fernTarget(t), "-o", bin,
		filepath.Join(root, "coreutils", util+".fern"),
		filepath.Join(root, "internal", "stdlib"))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host compile coreutils/%s.fern: %v\n%s", util, err, out)
	}
	selfHostBins[util] = bin
	return bin
}

func TestSelfHostCoreutilsParity(t *testing.T) {
	for _, util := range utilNames(t) {
		cases, ok := corpusRegistry[util]
		if !ok {
			continue
		}
		t.Run(util, func(t *testing.T) {
			native := fernBin(t, util)
			ours := selfHostBin(t, util)
			for _, inv := range cases(t) {
				t.Run(inv.name, func(t *testing.T) {
					if inv.stdoutPath == "" && len(inv.follow) == 0 &&
						inv.prepare == nil && len(inv.artifacts) == 0 {
						t.Parallel()
					}
					inv := inv.own(t)
					inv.prep(t)
					want := inv.run(t, native, util)
					wantFiles := inv.readArtifacts(t)
					inv.prep(t)
					got := inv.run(t, ours, util)
					diffArtifacts(t, util, inv, wantFiles, inv.readArtifacts(t), "native", "selfhost")
					if !sameOutput(inv, want.stdout, got.stdout) {
						t.Errorf("stdout differs for %s %s%s", util, quoteArgs(inv.args), diffBody("native", "selfhost", want.stdout, got.stdout))
					}
					if !sameOutput(inv, want.stderr, got.stderr) {
						t.Errorf("stderr differs for %s %s%s", util, quoteArgs(inv.args), diffBody("native", "selfhost", want.stderr, got.stderr))
					}
					if want.how() != got.how() {
						t.Errorf("status differs for %s %s: native %s, selfhost %s", util, quoteArgs(inv.args), want.how(), got.how())
					}
					if diff := treeDiff(want.tree, got.tree, "native", "selfhost"); diff != "" {
						t.Errorf("the files left behind differ for %s %s\n%s", util, quoteArgs(inv.args), diff)
					}
				})
			}
		})
	}
}

func TestSelfHostMulticallCompilesWhole(t *testing.T) {
	root := repoRoot(t)
	srcDir := filepath.Join(root, "coreutils", "multicall")
	e2eharness.TrackFernSources(t, srcDir, "fern-coreutils.fern")
	argv := crossArgv(selfHostCompiler(t), "-O", "-target", fernTarget(t), "-o", filepath.Join(t.TempDir(), "fern-coreutils"),
		filepath.Join(srcDir, "fern-coreutils.fern"),
		filepath.Join(root, "internal", "stdlib"))
	cmd := exec.Command(argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host compile of the multicall binary: %v\n%s", err, out)
	}
}
