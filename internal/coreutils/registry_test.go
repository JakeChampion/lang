package coreutils

import "testing"

// The corpus registry replaced a single map literal that every coreutils PR
// appended to (#8840). Two properties came free from that literal and have to
// be asserted now that it is gone: the compiler refused a duplicate key, and
// writing a key at all required naming it next to every other one, so a key
// for a utility that no longer exists was visible.

// registryProbe is not a legal file name on any target, so registering it
// cannot collide with a real utility.
const registryProbe = "\x00probe"

func TestRegisterCorpusRejectsDuplicates(t *testing.T) {
	cases := func(*testing.T) []invocation { return nil }
	registerCorpus(registryProbe, cases)
	defer delete(corpusRegistry, registryProbe)

	defer func() {
		if recover() == nil {
			t.Error("registerCorpus accepted a second registration for one utility; the duplicate-key refusal the map literal used to get from the compiler is gone")
		}
	}()
	registerCorpus(registryProbe, cases)
}

// TestCorpusRegistryHasNoStrays is TestSelfHostCoreutilsCoverage's other
// direction: a registration whose utility was deleted would otherwise sit in
// the registry unreferenced, and the self-host leg would never run it.
func TestCorpusRegistryHasNoStrays(t *testing.T) {
	have := make(map[string]bool, len(corpusRegistry))
	for _, util := range utilNames(t) {
		have[util] = true
	}
	for util := range corpusRegistry {
		if !have[util] {
			t.Errorf("registerCorpus(%q, ...) has no coreutils/%s.fern — drop the registration with the utility", util, util)
		}
	}
}
