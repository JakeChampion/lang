package e2eharness

import (
	"slices"
	"strings"
	"testing"
)

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
