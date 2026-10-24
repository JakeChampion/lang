package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostGenericCallArg(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.GenericCallArgProgram, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
