package e2eharness

import (
	"slices"
	"strings"
	"testing"
)

// A BoxedProbes test compiles its programs with the inliner off, but the
// driver it builds is cached under a key with no FERN_* component, so the
// setting must stay out of that build or every other test reads a boxed
// driver back.
func TestBoxedProbesReachProgramsNotDriverBuilds(t *testing.T) {
	if slices.Contains(ChildEnv(), BoxedProbe) {
		t.Fatalf("ChildEnv carries %s outside a BoxedProbes test", BoxedProbe)
	}
	BoxedProbes(t)
	if !slices.Contains(ChildEnv(), BoxedProbe) {
		t.Errorf("ChildEnv lacks %s in a BoxedProbes test", BoxedProbe)
	}
	if !slices.Contains(SelfHostChildEnv(), BoxedProbe) {
		t.Errorf("SelfHostChildEnv lacks %s in a BoxedProbes test", BoxedProbe)
	}
	for _, kv := range strippedEnv() {
		if strings.HasPrefix(kv, "FERN_") {
			t.Errorf("the driver build's env carries %s", kv)
		}
	}
}
