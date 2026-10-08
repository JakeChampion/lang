package e2ecompiler

import (
	"path/filepath"
	"testing"
)

func TestSelfHostGuardedCreditAlgebra(t *testing.T) {
	path, err := filepath.Abs("../../../compiler/drivers/ssacreditguard_run.fern")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("guarded credit controls exited %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
