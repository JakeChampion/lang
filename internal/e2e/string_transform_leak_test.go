package e2e

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestStringTransformInvolutionDoesNotLeak(t *testing.T) {
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("requires x86-64 tooling")
	}
	n, sites, err := traceOneFixture(t, gcc, runner, filepath.Join(conformanceCases, "prop_string_involution"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d unpaired allocations: %s", n, sites)
	}
}
