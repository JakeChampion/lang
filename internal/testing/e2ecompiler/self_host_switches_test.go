package e2ecompiler

import (
	"path/filepath"
	"testing"
)

// The retained-body drivers pass an explicit asmcore.Switches and run with
// whatever environment the test inherits, so a switch read from the
// environment behind that value changes the text their machines check.
func TestSelfHostExplicitSwitchesIgnoreTheEnvironment(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path, err := filepath.Abs("../../../compiler/drivers/switches_run.fern")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FERN_LEAKCHECK", "1")
	t.Setenv("FERN_RC_FREE_DEBUG", "1")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("switches_run exited %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
