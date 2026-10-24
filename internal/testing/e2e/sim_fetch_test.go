package e2e

import (
	"os"
	"strings"
	"testing"
)

const simFetchSuite = "tests/stdlib/sim_fetch_test.fern"

var simFetchWant = []string{"# Suite: std/sim_fetch", "# pass 53", "# fail 0", "1..53"}

func checkSimFetchOutput(t *testing.T, leg, out string) {
	t.Helper()
	for _, w := range simFetchWant {
		if !strings.Contains(out, w) {
			t.Errorf("%s: output missing %q\nfull output:\n%s", leg, w, out)
		}
	}
}

func TestRunnerSimFetchExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	code, out, errOut := runLangInterp(t, bin, langSrcAbs(t, simFetchSuite))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	checkSimFetchOutput(t, "interp", out)
}

func simFetchSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, simFetchSuite))
	if err != nil {
		t.Fatalf("read %s: %v", simFetchSuite, err)
	}
	return string(src)
}

func TestSimFetchNativeX86_64(t *testing.T) {
	out, code := compileAndRunX86_64(t, simFetchSource(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchOutput(t, "x86-64", out)
}

func TestSimFetchArm64(t *testing.T) {
	out, code := compileAndRunArm64(t, simFetchSource(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchOutput(t, "arm64", out)
}

func TestWASMSimFetch(t *testing.T) {
	out, _, code := runWasmArtifact(t, buildResultComponent(t, simFetchSource(t)), runOpts{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchOutput(t, "wasm", out)
}
