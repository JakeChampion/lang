package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The conflict sweep is a step of main-housekeeping.yml, the one job a push to
// main runs for its housekeeping, beside the stale-run reap and check-sources.
func TestCancelOnConflictReapsSafely(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "main-housekeeping.yml"))
	if err != nil {
		t.Fatalf("read main-housekeeping.yml: %v", err)
	}
	src := string(b)

	if _, ok := topLevelBlock(src, "concurrency"); ok {
		t.Error("main-housekeeping.yml has a concurrency group: under steady merges " +
			"each pending run is replaced by the next and the sweep never runs")
	}
	on, ok := onBlock(src)
	if !ok {
		t.Fatal("main-housekeeping.yml has no `on:` block")
	}
	if push, ok := triggerBlock(on, "push"); !ok || !strings.Contains(push, "branches: [main]") {
		t.Error("main-housekeeping.yml does not run on a push to main, the moment a pull request can start conflicting")
	}
	for _, step := range []string{"uses: ./.github/actions/reap-stale-runs", "run: make check-sources"} {
		if !strings.Contains(src, step) {
			t.Errorf("main-housekeeping.yml lost the step %q: a push to main would need a job of its own for it again", step)
		}
	}

	if !strings.Contains(src, "fetch-depth: 0") {
		t.Error("the checkout is shallow: merge-tree finds no merge base")
	}

	if !strings.Contains(src, `git("rev-parse", "FETCH_HEAD")`) {
		t.Error("the base is not read from the fetched main tip")
	}
	if !regexp.MustCompile(`git\("merge-tree"[^)]*, baseSha, head\)`).MatchString(src) {
		t.Error("merge-tree does not merge the PR head into the fetched main tip")
	}

	list := regexp.MustCompile(`listWorkflowRuns\(\{[^}]*\}`).FindString(src)
	for _, want := range []string{`workflow_id: "ci.yml"`, `event: "pull_request"`, `head_sha: head`} {
		if !strings.Contains(list, want) {
			t.Errorf("listWorkflowRuns is missing %s: it would cancel runs that are not the conflicting PR's CI", want)
		}
	}

	if !regexp.MustCompile("const marker = \\(sha\\) =>[^;]*\\$\\{sha\\.slice").MatchString(src) {
		t.Error("the comment marker is not keyed on the PR's head SHA")
	}
}
