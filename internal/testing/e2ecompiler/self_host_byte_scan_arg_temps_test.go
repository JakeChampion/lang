package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostByteScanArgTempsAreReleased is the self-host twin of
// internal/testing/e2e's TestByteScanArgTempsAreReleased.
func TestSelfHostByteScanArgTempsAreReleased(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.ByteScanArgTempsProgram, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
