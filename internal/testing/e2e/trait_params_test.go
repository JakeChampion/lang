package e2e

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestTraitParams(t *testing.T) {
	main := e2eharness.WriteTraitParamsProject(t)
	t.Run("interp", func(t *testing.T) {
		out, err := exec.Command(buildLangBinForInterp(t), "-interp", main).CombinedOutput()
		if err != nil {
			t.Fatalf("interp: %v\n%s", err, out)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if out, code := runFixtureX86_64(t, main, ""); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if out, code := runFixtureArm64(t, main, ""); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if out, code := runFixtureWasm(t, main, ""); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
	})
}
