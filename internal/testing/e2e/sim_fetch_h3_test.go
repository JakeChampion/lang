package e2e

import (
	"os"
	"strings"
	"testing"
)

// The std/fetch client over HTTP/3 on the simulated network: requests to an
// origin that offers HTTP/3 go over QUIC, the pool keeps the connection,
// lost datagrams are recovered, a kept ticket sends a GET in 0-RTT, a 425
// Too Early is answered by a second send, an alternative that does not
// answer falls back to TCP, and Alt-Svc field values are read and aged.
const simFetchH3Suite = "tests/stdlib/sim_fetch_h3_test.fern"

var simFetchH3Want = []string{"# Suite: std/fetch over HTTP/3", "# pass 10", "# fail 0", "1..10"}

func checkSimFetchH3Output(t *testing.T, leg, out string) {
	t.Helper()
	for _, w := range simFetchH3Want {
		if !strings.Contains(out, w) {
			t.Errorf("%s: output missing %q\nfull output:\n%s", leg, w, out)
		}
	}
}

func simFetchH3Source(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, simFetchH3Suite))
	if err != nil {
		t.Fatalf("read %s: %v", simFetchH3Suite, err)
	}
	return string(src)
}

func TestRunnerSimFetchH3Passes(t *testing.T) {
	bin := buildLangBinForInterp(t)
	code, out, errOut := runLangInterp(t, bin, langSrcAbs(t, simFetchH3Suite))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	checkSimFetchH3Output(t, "interp", out)
}

func TestSimFetchH3NativeX86_64(t *testing.T) {
	out, code := compileAndRunX86_64(t, simFetchH3Source(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchH3Output(t, "x86-64", out)
}

func TestSimFetchH3Arm64(t *testing.T) {
	out, code := compileAndRunArm64(t, simFetchH3Source(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchH3Output(t, "arm64", out)
}

func TestWASMSimFetchH3(t *testing.T) {
	out, _, code := runWasmArtifact(t, buildResultComponent(t, simFetchH3Source(t)), runOpts{})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimFetchH3Output(t, "wasm", out)
}
