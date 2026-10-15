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

// TestSelfHostCheckAgreesWithGoOnTheCorpus holds the self-host compiler's
// `-check`, the one `fern -check` runs, to the Go checker's verdict on every
// program the repository keeps that is meant to compile: the conformance cases
// without an expected error, tests/ and examples/. A program one accepts and
// the other refuses fails here, in either direction (#12382).
//
// The expected.error cases are TestSelfHostRejectsConformanceErrorCasesX86_64's.
func TestSelfHostCheckAgreesWithGoOnTheCorpus(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	progs := checkCorpus(t)
	if len(progs) < 800 {
		t.Fatalf("found only %d corpus programs — the corpus moved and this gate proves little", len(progs))
	}
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

// checkCorpus is the corpus, as paths relative to the repository root.
func checkCorpus(t *testing.T) []string {
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
	for _, top := range []string{"tests", "examples"} {
		err := filepath.WalkDir(e2eharness.RepoPath(top), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
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
