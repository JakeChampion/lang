package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostStringCharacterSet(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, e2eharness.StringCharacterSetProgram, target); code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
