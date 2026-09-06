package sourcelint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The doc-only exclusion every heavy pull_request lane carries. Narrow on
// purpose: `*.md` does not cross a `/` in a GitHub path filter, so it covers
// root-level prose and leaves the literate sources under
// examples/literate/*.fern.md — which are compiled — triggering everything.
var docOnlyPathsIgnore = []string{
	"docs/**",
	"*.md",
}

// alwaysRunOnPR names the lane that must NEVER carry the filter. A doc-only PR
// still has to run one real gate: a pull request reporting no checks at all is
// indistinguishable from one whose CI never fired, and this repo merges on the
// checks list.
const alwaysRunOnPR = "lint.yml"

// Every workflow that runs on `pull_request` either scopes itself with its own
// `paths:` allowlist or carries the shared doc-only `paths-ignore`. The lane in
// alwaysRunOnPR is the single deliberate exception.
//
// This is a drift guard, and the drift it guards against has bitten here
// before in the same shape: a list maintained by hand in one place while the
// thing it describes grows somewhere else (see scripts/unit-test-packages for
// the version of this that cost four rounds of silently-unrun tests). A new
// PR-triggered workflow added without a filter is a workflow that runs the
// whole compiler suite on a typo fix, and nothing else would say so.
func TestPRWorkflowsShareOneDocOnlyFilter(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}

	var checked []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(b)

		on, ok := onBlock(src)
		if !ok || !strings.Contains(on, "pull_request:") {
			continue // not a pull_request lane
		}
		// `pull_request: types: [closed]` (cancel-on-merge) is a lifecycle
		// hook, not a test lane: it fires once when a PR closes and launches
		// no suite.
		if strings.Contains(on, "types: [closed]") {
			continue
		}
		checked = append(checked, e.Name())

		hasIgnore := strings.Contains(on, "paths-ignore:")
		// A `paths:` allowlist already scopes the lane to the sources it
		// gates, which is strictly narrower than the doc-only exclusion.
		hasAllowlist := strings.Contains(on, "paths:")

		if e.Name() == alwaysRunOnPR {
			if hasIgnore || hasAllowlist {
				t.Errorf("%s must run on every pull request: it is the one gate a "+
					"doc-only PR still reports, but it carries a path filter", e.Name())
			}
			continue
		}
		if hasAllowlist {
			continue
		}
		if !hasIgnore {
			t.Errorf("%s runs on pull_request with no path filter: a doc-only PR "+
				"would launch it. Add the shared paths-ignore block (see any "+
				"test-e2e-*.yml) or a `paths:` allowlist.", e.Name())
			continue
		}
		for _, want := range docOnlyPathsIgnore {
			if !strings.Contains(on, `- "`+want+`"`) {
				t.Errorf("%s: paths-ignore is missing %q — the block must be identical "+
					"across lanes, or a doc-only PR fires some of them and not others",
					e.Name(), want)
			}
		}
	}

	if len(checked) == 0 {
		t.Fatal("no pull_request workflows found — did the `on:` format change?")
	}
	sort.Strings(checked)
	found := false
	for _, n := range checked {
		if n == alwaysRunOnPR {
			found = true
		}
	}
	if !found {
		t.Errorf("%s is not among the pull_request workflows (%v) — the doc-only PR "+
			"has no gate left", alwaysRunOnPR, checked)
	}
}

// onBlock returns the workflow's top-level `on:` mapping: everything from the
// `on:` line to the next line that starts in column zero. Path filters and
// trigger types are nested inside it, so matching within this block cannot pick
// up a `paths:` that belongs to a step.
func onBlock(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if l == "on:" || strings.HasPrefix(l, "on: ") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

// lifecycleHookLanes are `pull_request` workflows that launch no test suite, so
// there is nothing for a main run to prove.
var lifecycleHookLanes = map[string]bool{
	// Fires once when a PR closes, to reap that branch's orphaned runs.
	"cancel-on-merge.yml": true,
}

// Every gate lane runs on main as well as on the pull request, with the same
// path filter on both.
//
// PRs here are rebase-merged, so each commit lands on a main that its own PR's
// CI never saw: a coupling between two individually-green PRs exists only in
// the combination, and only a run against the merge reports it. For a long
// time only test-units.yml and check-sources.yml covered main, which left the
// compiler suite — every e2e lane, the fuzzers, the fixpoints — proving
// something about PR heads and nothing at all about the branch people ship
// from. A red main was discoverable only by dispatching the lanes by hand.
//
// The two halves are checked together because either alone is a silent gap: a
// push trigger whose filter has drifted from the pull_request one runs a
// different set of merges than it runs PRs, which is worse than not running.
func TestGateLanesRunOnMain(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}

	var checked []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(b)

		on, ok := onBlock(src)
		if !ok || !strings.Contains(on, "pull_request:") {
			continue
		}
		if lifecycleHookLanes[e.Name()] || strings.Contains(on, "types: [closed]") {
			continue
		}
		checked = append(checked, e.Name())

		push, ok := triggerBlock(on, "push")
		if !ok {
			t.Errorf("%s gates pull requests but not main: add a `push: branches: [main]` "+
				"trigger mirroring its pull_request filter, or list it in "+
				"lifecycleHookLanes if it launches no suite", e.Name())
			continue
		}
		if !strings.Contains(push, "branches: [main]") {
			t.Errorf("%s: push trigger is not scoped to main — every branch would "+
				"fire it twice, once for the push and once for the PR", e.Name())
		}

		pr, ok := triggerBlock(on, "pull_request")
		if !ok {
			t.Errorf("%s: pull_request trigger found by substring but not by block — "+
				"did the `on:` format change?", e.Name())
			continue
		}
		if got, want := pathFilter(push), pathFilter(pr); got != want {
			t.Errorf("%s: the push filter has drifted from the pull_request one, so "+
				"main and PRs run different sets of changes\n  push:         %s\n  pull_request: %s",
				e.Name(), got, want)
		}

		// Whether main cancels its own runs is settled in
		// main_concurrency_test.go, which states the policy for every main lane
		// rather than for gate lanes alone. What stays here is the grouping KEY,
		// which is what makes cancellation coalesce instead of misfiring.
		if conc, ok := concurrencyBlock(src); ok {
			if strings.Contains(conc, "github.sha") {
				t.Errorf("%s: main is keyed on the SHA, so every merge gets its own "+
					"concurrency group and supersedes nothing. A burst of rebase-merges "+
					"then holds an uncancellable run each, ahead of every open PR "+
					"(#8124). Key main on the ref (see test-units.yml) so a burst "+
					"coalesces to its newest commit", e.Name())
			}
		}
	}

	if len(checked) == 0 {
		t.Fatal("no pull_request workflows found — did the `on:` format change?")
	}
	sort.Strings(checked)
}

// triggerBlock returns the body of one trigger inside an `on:` mapping —
// everything indented under `  <name>:` up to the next key at that indent.
func triggerBlock(on, name string) (string, bool) {
	lines := strings.Split(on, "\n")
	start := -1
	for i, l := range lines {
		if l == "  "+name+":" || strings.HasPrefix(l, "  "+name+": ") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "    ") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

// concurrencyBlock returns the body of the workflow-level `concurrency:`
// mapping. Scoped rather than matched against the whole file so a job-level
// group, or the word in a comment, is not read as the workflow's own.
func concurrencyBlock(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if l == "concurrency:" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, " ") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

// pathFilter reduces a trigger body to its filter kind plus its quoted
// entries, so two triggers compare equal when they select the same changes
// however their comments and blank lines differ.
func pathFilter(block string) string {
	kind := "(none)"
	var entries []string
	for _, l := range strings.Split(block, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "paths:" || t == "paths-ignore:":
			kind = t
		case strings.HasPrefix(t, `- "`):
			entries = append(entries, strings.TrimPrefix(t, "- "))
		}
	}
	return kind + " " + strings.Join(entries, " ")
}
