package coreutils

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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

	unclaimed := make(map[string]bool, len(have))
	for name := range have {
		unclaimed[name] = true
	}

	sourced := map[string]bool{}
	for _, util := range utilNames(t) {
		if !have[util] {
			t.Errorf("coreutils/%s.fern has no bench workloads — add scripts/coreutils-bench.d/%s.sh", util, util)
			continue
		}
		delete(unclaimed, util)
		body, err := os.ReadFile(filepath.Join(dir, util+".sh"))
		if err != nil {
			t.Errorf("read %s.sh: %v", util, err)
			continue
		}
		for _, m := range benchSharedSource.FindAllStringSubmatch(string(body), -1) {
			sourced[m[1]] = true
		}
	}

	for name := range unclaimed {
		if !strings.HasPrefix(name, "_") {
			t.Errorf("scripts/coreutils-bench.d/%s.sh has no coreutils/%s.fern — drop the workloads with the utility", name, name)
		} else if !sourced[name] {
			t.Errorf("scripts/coreutils-bench.d/%s.sh is sourced by no utility — drop it with the last member that used it", name)
		}
	}
	for name := range sourced {
		if !have[name] {
			t.Errorf("a utility sources scripts/coreutils-bench.d/%s.sh, which does not exist", name)
		}
	}
}

var benchSharedSource = regexp.MustCompile(`(?m)^\.[ \t]+"?scripts/coreutils-bench\.d/(_[A-Za-z0-9_]+)\.sh"?[ \t]*$`)
