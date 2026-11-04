package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostPatternPayloadCopy(t *testing.T) {
	prog, err := os.ReadFile(filepath.Join(langSrcAbs(t, "conformance"), "cases", "let_else_payload_copy", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, string(prog), target, "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0 (99 is an RC underflow)\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
