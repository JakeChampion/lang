package e2e

import (
	"os"
	"strings"
	"testing"
)

// `examples/tests/sim_fetch_test.fern` runs std/fetch's client over the
// scripted network in std/sim_fetch: every behaviour of the dialled route
// (bounds at their exact virtual time, redirects, the reset retry, the
// block list, decoding, the body caps) against a transport the test
// scripts. It is the client's sim parity suite, so it runs on every
// backend, and on the self-host compiler in internal/e2eselfhost's
// std/test e2e.

const simFetchSuite = "examples/tests/sim_fetch_test.fern"

var simFetchWant = []string{"# Suite: std/sim_fetch", "# pass 29", "# fail 0", "1..29"}

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
	out, code := compileAndRunX86Native(t, simFetchSource(t))
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
	out, code := runFixtureWasm(t, langSrcAbs(t, simFetchSuite), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchOutput(t, "wasm", out)
}
