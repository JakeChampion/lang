package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host twin of TestTraitParams: the trait-typed parameters desugar to
// anonymous generics through the CLI's module loading and flattening.
func TestSelfHostTraitParams(t *testing.T) {
	cli := buildSelfHostCLI(t)
	main := e2eharness.WriteTraitParamsProject(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, main, target, nil, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
