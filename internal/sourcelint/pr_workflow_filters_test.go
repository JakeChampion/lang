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
	"LICENSE",
	".github/ISSUE_TEMPLATE/**",
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
