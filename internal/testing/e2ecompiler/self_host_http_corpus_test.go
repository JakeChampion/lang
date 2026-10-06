package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostHTTPCorpus is the self-host twin of internal/testing/e2e's
// TestHTTPCorpus: the parser corpus program, compiled by the self-host
// compiler for x86-64, prints the pinned verdict for every request.
func TestSelfHostHTTPCorpus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	gcc, runner := x86_64Tooling(t)
	_ = gcc
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	cases := e2eharness.HTTPCorpusCases(t, "../e2e/testdata/http-corpus")
	src := filepath.Join(dir, "corpus.fern")
	if err := os.WriteFile(src, []byte(e2eharness.HTTPCorpusSource(cases)), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "corpus")
	cmd := exec.Command(driver, "-target", "x86-64-linux", "-o", bin, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if report, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, report)
	}
	out, err := runX86_64Bin(runner, bin).Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if diffs := e2eharness.HTTPCorpusDiff(cases, string(out)); len(diffs) > 0 {
		t.Fatalf("self-host disagrees with the pinned verdicts:\n%s", strings.Join(diffs, "\n"))
	}
}
