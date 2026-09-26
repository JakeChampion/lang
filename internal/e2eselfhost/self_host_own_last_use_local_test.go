package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostOwnLastUseLocal runs the conformance case for a local handed to
// an `own` parameter where it dies (#9541) through the self-host CLI with the
// leak census on. The fixture legs check its exit code; this checks that the
// semantic lowering moves each local exactly once — a reference released
// twice or never shows here and nowhere else on the self-host path.
func TestSelfHostOwnLastUseLocal(t *testing.T) {
	prog, err := os.ReadFile(filepath.Join(langSrcAbs(t, "conformance"), "cases", "own_param_last_use_local", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, string(prog), target, "FERN_LEAKCHECK=1")
			if code != 88 {
				t.Fatalf("exit %d, want 88 (99 is an rc underflow)\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
