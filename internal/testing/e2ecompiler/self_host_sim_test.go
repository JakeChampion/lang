package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host twins of the sim_net and sim_fault native programs: std/sim's
// scripted endpoints and seed-driven faults are pure computation, so the
// self-host build must reach every check (exit 42) on both native targets.
func TestSelfHostSimPrograms(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, src string }{
		{"net", e2eharness.SimNetProgram},
		{"fault", e2eharness.SimFaultProgram},
	} {
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target, "FERN_STRICT_IR=1"); code != 42 {
					t.Fatalf("exit = %d, want 42 (failing check index)\n%s", code, stderr)
				}
			})
		}
	}
}
