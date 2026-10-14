package e2ecompiler

import (
	"path/filepath"
	"testing"
)

// The retained-body controls and the text machines are two drivers: under
// the sanitizer each process keeps every block it frees.
func TestSelfHostRetainedNativeBodies(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, driver := range []string{"ssaretained_run", "ssaretainedmachines_run"} {
		path, err := filepath.Abs("../../../compiler/drivers/" + driver + ".fern")
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi"} {
			t.Run(driver+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("%s exited %d: %s", driver, code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
