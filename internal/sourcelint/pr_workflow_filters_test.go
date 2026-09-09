package sourcelint

import (
	"strings"
	"testing"
)

// The doc-only exclusion every heavy lane carries. Narrow on purpose: `*.md`
// does not cross a `/` in a GitHub path filter, so it covers root-level prose
// and leaves the literate sources under examples/literate/*.fern.md — which
// are compiled — triggering everything.
var docOnlyPathsIgnore = []string{
	"docs/**",
	"*.md",
}

// alwaysRunOnPR names the lane that must NEVER carry a filter. A doc-only PR
// still has to run one real gate: a pull request reporting no checks at all is
// indistinguishable from one whose CI never fired, and this repo merges on the
// checks list.
const alwaysRunOnPR = "lint"

// Every lane in ci.yml's table either scopes itself with a `paths:` allowlist
// or carries the shared doc-only `paths-ignore`, and the two are exclusive. The
// lane in alwaysRunOnPR is the single deliberate exception, and it is kept out
// of the table altogether (TestCILaneFiltersMatchTheirJobs).
//
// This is a drift guard, and the drift it guards against has bitten here
// before in the same shape: a list maintained by hand in one place while the
// thing it describes grows somewhere else (see scripts/unit-test-packages for
// the version of this that cost four rounds of silently-unrun tests). A lane
// row with an empty filter is a lane that runs the whole compiler suite on a
// typo fix, and nothing else would say so.
func TestPRLanesShareOneDocOnlyFilter(t *testing.T) {
	for lane, f := range laneTable(t) {
		switch {
		case len(f.Paths) > 0 && len(f.PathsIgnore) > 0:
			t.Errorf("%s: %q carries both `paths` and `paths-ignore`; GitHub requires both to "+
				"be satisfied, which no lane here means", ciFile, lane)
		case len(f.Paths) > 0:
			// An allowlist already scopes the lane to the sources it gates,
			// which is strictly narrower than the doc-only exclusion.
		case len(f.PathsIgnore) == 0:
			t.Errorf("%s: %q has no path filter: a doc-only PR would launch it. Give it the "+
				"shared paths-ignore block or a `paths` allowlist.", ciFile, lane)
		case strings.Join(f.PathsIgnore, " ") != strings.Join(docOnlyPathsIgnore, " "):
			t.Errorf("%s: %q's paths-ignore is %v, not %v — the block must be identical "+
				"across lanes, or a doc-only PR fires some of them and not others",
				ciFile, lane, f.PathsIgnore, docOnlyPathsIgnore)
		}
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

// Every gate lane runs on main as well as on the pull request, under the same
// path filter.
//
// PRs here are rebase-merged, so each commit lands on a main that its own PR's
// CI never saw: a coupling between two individually-green PRs exists only in
// the combination, and only a run against the merge reports it. For a long
// time only test-units.yml and check-sources.yml covered main, which left the
// compiler suite — every e2e lane, the fuzzers, the fixpoints — proving
// something about PR heads and nothing at all about the branch people ship
// from. A red main was discoverable only by dispatching the lanes by hand.
//
// Both halves now come from one workflow: ci.yml triggers on both events and
// its lane table is the one filter either event reads, so the filters cannot
// drift apart — provided neither trigger grows a filter of its own on top.
func TestGateLanesRunOnMain(t *testing.T) {
	src := workflowSource(t, ciFile)
	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", ciFile)
	}
	pr, ok := triggerBlock(on, "pull_request")
	if !ok {
		t.Fatalf("%s does not trigger on pull_request — nothing gates a pull request", ciFile)
	}
	push, ok := triggerBlock(on, "push")
	if !ok {
		t.Fatalf("%s gates pull requests but not main: add a `push: branches: [main]` "+
			"trigger — every lane must run against the merge too", ciFile)
	}
	if !strings.Contains(push, "branches: [main]") {
		t.Errorf("%s: push trigger is not scoped to main — every branch would fire it "+
			"twice, once for the push and once for the PR", ciFile)
	}
	for name, block := range map[string]string{"pull_request": pr, "push": push} {
		if strings.Contains(block, "paths") {
			t.Errorf("%s: the %s trigger carries a path filter of its own. The lane table "+
				"is the one filter both events read; a filter here applies to one event "+
				"only, and main and PRs run different sets of changes", ciFile, name)
		}
	}

	// Whether main cancels its own runs is settled in main_concurrency_test.go.
	// What stays here is the grouping KEY, which is what makes a burst of
	// merges queue instead of each holding a run.
	if conc, ok := concurrencyBlock(src); ok && strings.Contains(conc, "github.sha") {
		t.Errorf("%s: main is keyed on the SHA, so every merge gets its own concurrency "+
			"group and waits on nothing. A burst of rebase-merges then holds an "+
			"uncancellable run each, ahead of every open PR (#8124). Key main on the "+
			"ref so a burst queues", ciFile)
	}
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
