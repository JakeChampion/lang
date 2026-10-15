package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostCheckAgreesWithGo* hold the self-host compiler's `-check`, the
// one `fern -check` runs, to the Go checker's verdict on every program the
// repository keeps that is meant to compile: the conformance cases without an
// expected error, tests/ and examples/. A program one accepts and the other
// refuses fails here, in either direction (#12382). The corpus is split so the
// sharder can place the parts apart; together they are more than half a
// shard's budget, and tests/stdlib is two thirds of it.
//
// The expected.error cases are TestSelfHostRejectsConformanceErrorCasesX86_64's.
func TestSelfHostCheckAgreesWithGoOnConformance(t *testing.T) {
	checkAgreesWithGo(t, conformanceCorpus(t), 500)
}

func TestSelfHostCheckAgreesWithGoOnStdlibTests(t *testing.T) {
	checkAgreesWithGo(t, treeCorpus(t, filepath.Join("tests", "stdlib"), ""), 200)
}

func TestSelfHostCheckAgreesWithGoOnTests(t *testing.T) {
	checkAgreesWithGo(t, treeCorpus(t, "tests", filepath.Join("tests", "stdlib")), 50)
}

func TestSelfHostCheckAgreesWithGoOnExamples(t *testing.T) {
	checkAgreesWithGo(t, treeCorpus(t, "examples", ""), 100)
}

// checkAgreesWithGo checks each program, paths relative to the repository
// root, under both checkers. Fewer than `least` programs means the corpus
// moved and the gate proves little.
func checkAgreesWithGo(t *testing.T, progs []string, least int) {
	if len(progs) < least {
		t.Fatalf("found only %d corpus programs — the corpus moved and this gate proves little", len(progs))
	}
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	for _, rel := range progs {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			path := e2eharness.RepoPath(rel)
			goOut, goOK := checkVerdict(exec.Command(oracle, "-check", path))
			shOut, shOK := checkVerdict(exec.Command(cli, "-check", path, root))
			if goOK != shOK {
				t.Errorf("the Go checker %s it and the self-host %s it\n--- Go ---\n%s--- self-host ---\n%s",
					verdictWord(goOK), verdictWord(shOK), goOut, shOut)
			}
		})
	}
}

// conformanceCorpus is the conformance cases that are meant to compile.
func conformanceCorpus(t *testing.T) []string {
	t.Helper()
	var out []string
	cases, err := filepath.Glob(e2eharness.RepoPath("conformance", "cases", "*", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cases {
		dir := filepath.Dir(p)
		if fileExists(filepath.Join(dir, "expected.error")) || fileExists(filepath.Join(dir, "expected.lowering-error")) {
			continue
		}
		out = append(out, filepath.Join("conformance", "cases", filepath.Base(dir), "main.fern"))
	}
	sort.Strings(out)
	return out
}

// treeCorpus is every .fern file under the repository directory `top`, less
// those under `skip` when it is not empty.
func treeCorpus(t *testing.T, top, skip string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(e2eharness.RepoPath(top), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skip != "" && p == e2eharness.RepoPath(skip) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".fern") {
			rel, _ := filepath.Rel(e2eharness.RepoPath(), p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// checkVerdict runs a `-check` and reports its output and whether it accepted.
func checkVerdict(cmd *exec.Cmd) (string, bool) {
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func verdictWord(ok bool) string {
	if ok {
		return "accepts"
	}
	return "refuses"
}
