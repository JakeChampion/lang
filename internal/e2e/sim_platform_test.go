package e2e

import (
	"os"
	"strings"
	"testing"
)

// `examples/tests/sim_platform_test.fern` runs a handler's platform over
// the simulation (std/sim_platform): the clock, randomness, configuration
// and the public-only HTTP route answered from a `Sim`, a scripted network
// and what the test set, with the shapes the host and the mock answer. It
// is the platform's sim parity suite (#9855), so it runs on every backend,
// and on the self-host compiler in internal/e2eselfhost's std/test e2e.

const simPlatformSuite = "examples/tests/sim_platform_test.fern"

var simPlatformWant = []string{"# Suite: std/sim_platform", "# pass 8", "# fail 0", "1..8"}

func checkSimPlatformOutput(t *testing.T, leg, out string) {
	t.Helper()
	for _, w := range simPlatformWant {
		if !strings.Contains(out, w) {
			t.Errorf("%s: output missing %q\nfull output:\n%s", leg, w, out)
		}
	}
}

func TestRunnerSimPlatformExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	code, out, errOut := runLangInterp(t, bin, langSrcAbs(t, simPlatformSuite))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	checkSimPlatformOutput(t, "interp", out)
}

func simPlatformSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, simPlatformSuite))
	if err != nil {
		t.Fatalf("read %s: %v", simPlatformSuite, err)
	}
	return string(src)
}

func TestSimPlatformNativeX86_64(t *testing.T) {
	out, code := compileAndRunX86Native(t, simPlatformSource(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "x86-64", out)
}

func TestSimPlatformArm64(t *testing.T) {
	out, code := compileAndRunArm64(t, simPlatformSource(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "arm64", out)
}

func TestWASMSimPlatform(t *testing.T) {
	out, code := runFixtureWasm(t, langSrcAbs(t, simPlatformSuite), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "wasm", out)
}
