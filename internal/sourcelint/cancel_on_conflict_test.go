package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cancel-on-conflict.yml cancels CI and comments from an inline github-script
// body, so nothing type-checks the choices below and each failure is silent: a
// live run cancelled, a conflict missed, or the same comment on every merge.
func TestCancelOnConflictReapsSafely(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cancel-on-conflict.yml"))
	if err != nil {
		t.Fatalf("read cancel-on-conflict.yml: %v", err)
	}
	src := string(b)

	// merge-tree walks back to each PR's merge base with main; a shallow clone
	// has none and every PR reads as unrelated.
	if !strings.Contains(src, "fetch-depth: 0") {
		t.Error("the checkout is shallow: merge-tree finds no merge base")
	}

	// Main's tip at run time, not the pushed commit: a run that waited for a
	// runner would test a main that has already moved.
	if !strings.Contains(src, `git("rev-parse", "FETCH_HEAD")`) {
		t.Error("the base is not read from the fetched main tip")
	}
	if !regexp.MustCompile(`git\("merge-tree"[^)]*, baseSha, head\)`).MatchString(src) {
		t.Error("merge-tree does not merge the PR head into the fetched main tip")
	}

	// Only ci.yml runs at the conflicting SHA. Without the head_sha filter a
	// push that resolved the conflict would have its fresh run cancelled.
	list := regexp.MustCompile(`listWorkflowRuns\(\{[^}]*\}`).FindString(src)
	for _, want := range []string{`workflow_id: "ci.yml"`, `event: "pull_request"`, `head_sha: head`} {
		if !strings.Contains(list, want) {
			t.Errorf("listWorkflowRuns is missing %s: it would cancel runs that are not the conflicting PR's CI", want)
		}
	}

	// The dedupe key names the head, so main moving does not re-post and a new
	// conflicting push does.
	if !regexp.MustCompile("const marker = \\(sha\\) =>[^;]*\\$\\{sha\\.slice").MatchString(src) {
		t.Error("the comment marker is not keyed on the PR's head SHA")
	}
}
