package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The sibling of TestNoSilentlyCIDarkEnvGates. That one catches a test that
// never RUNS; this one catches a test that runs and cannot FAIL.
//
// Every test in this package drives a CI script whose behaviour is decided by
// the environment — FERN_CI_TOLERATE_VANISHED_SHARDS says whether a vanished
// shard is fatal, FERN_CI_JOBS_JSON substitutes a saved payload for the API
// call, FERN_CI_SIZE_GATE_STRICT turns findings into failures. Build the
// child environment with a bare os.Environ() and the ambient value decides
// what the test asserts, which is invisible: a vacuous test and a passing test
// are byte-identical in the log.
//
// It has happened twice. #6830 found the size gate reporting zero findings
// under an ambient FERN_SIZE_TOLERANCE_PERCENT=99, against a test asserting it
// reported some. #6833 found TestClassifyVanishedShardsIsAdvisory naming "a run
// id that cannot resolve" while an ambient FERN_CI_JOBS_JSON sent the script
// down the diagnose branch instead — exit 0 either way, so the assertion held
// and the named path was never reached.
//
// So: in this package a child environment is built with ciEnv, which strips
// GITHUB_* / RUNNER_* / FERN_* and then sets exactly what the test exercises.
// Iterating os.Environ() to filter it is how ciEnv is written and stays
// allowed; splicing it straight into a child environment does not.
//
// Scope is this package deliberately. The e2e trees also inherit (~23 sites),
// but most of those set the one variable they depend on and are fine as they
// are; sorting the rest needs reading rather than a rule, and #6833 tracks it.
var ambientEnvRe = regexp.MustCompile(`os\.Environ\(\)`)

// A filtering loop — `for _, kv := range os.Environ()` — is the helper's own
// shape and the point of the rule, not a violation of it.
var ambientFilterRe = regexp.MustCompile(`range\s+os\.Environ\(\)`)

func TestSourcelintChildEnvIsFiltered(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	dir := filepath.Join(root, "internal", "sourcelint")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var scanned int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		scanned++
		for i, line := range strings.Split(string(b), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if !ambientEnvRe.MatchString(code) || ambientFilterRe.MatchString(code) {
				continue
			}
			t.Errorf("%s:%d splices the inherited environment straight into a child:\n\t%s\n"+
				"Use ciEnv(...) instead — it strips GITHUB_* / RUNNER_* / FERN_* so an "+
				"ambient value cannot decide what this test asserts (#6833).",
				e.Name(), i+1, strings.TrimSpace(line))
		}
	}

	// A rule that scanned nothing would pass for the wrong reason — the same
	// failure it exists to prevent.
	if scanned == 0 {
		t.Fatal("scanned no _test.go files in internal/sourcelint")
	}
}
