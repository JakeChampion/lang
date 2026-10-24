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
