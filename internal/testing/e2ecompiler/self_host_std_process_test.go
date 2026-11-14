package e2ecompiler

import (
	"path/filepath"
	"testing"
)

// TestSelfHostStdProcess runs the tests of std/process on the native
// targets with the leak census. The interpreters and WASI cannot fork, so
// the suite is not a case of the std/test corpus.
func TestSelfHostStdProcess(t *testing.T) {
	path, err := filepath.Abs("../../../tests/stdlib/process_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
