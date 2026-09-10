package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// scripts/coreutils-bench had the same shape as the corpus map — one case
// statement per utility, in one file (#8840) — and is now one file per utility
// under scripts/coreutils-bench.d/. Missing one used to be a runtime error from
// the bench's default arm, which nobody sees until they benchmark; this is the
// same check, at test time.
//
// A `_`-prefixed file is a body shared by several utilities (the digests, the
// base encodings), each of which has its own file sourcing it. No coreutils
// utility name starts with `_`.
func TestCoreutilsBenchWorkloadCoverage(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "scripts", "coreutils-bench.d")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	have := make(map[string]bool, len(entries))
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sh")
		if name == e.Name() {
			t.Errorf("scripts/coreutils-bench.d/%s is not a .sh file", e.Name())
			continue
		}
		have[name] = true
	}

	for _, util := range utilNames(t) {
		if !have[util] {
			t.Errorf("coreutils/%s.fern has no bench workloads — add scripts/coreutils-bench.d/%s.sh", util, util)
		}
		delete(have, util)
	}
	for name := range have {
		if !strings.HasPrefix(name, "_") {
			t.Errorf("scripts/coreutils-bench.d/%s.sh has no coreutils/%s.fern — drop the workloads with the utility", name, name)
		}
	}
}
