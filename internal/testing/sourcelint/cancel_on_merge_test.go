package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCancelOnMergeReapsSafely(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "cancel-on-merge.yml"))
	if err != nil {
		t.Fatalf("read cancel-on-merge.yml: %v", err)
	}
	src := string(b)

	runs := strings.Index(src, "listWorkflowRunsForRepo")
	prs := strings.Index(src, "github.rest.pulls.list")
	switch {
	case runs < 0:
		t.Fatal("no listWorkflowRunsForRepo call")
	case prs < 0:
		t.Fatal("no pulls.list call")
	case runs > prs:
		t.Error("open PRs are listed before the runs: a force-push landing mid-job " +
			"leaves the live PR's new runs matching no open head SHA, and they get cancelled")
	}

	for _, status := range []string{"requested", "waiting", "pending", "queued", "in_progress"} {
		if !strings.Contains(src, `"`+status+`"`) {
			t.Errorf("status %q is never swept: runs in it outlive the PR that queued them", status)
		}
	}

	for _, want := range []struct{ needle, why string }{
		{"pr.merge_commit_sha", "the merge commit's tree is what decides whether the run tested what landed"},
		{`run.path !== ".github/workflows/ci.yml"`, "only the CI run is main's evidence; the branch's other runs are reaped as before"},
		{`j.name.startsWith("Full suite / ")`, "a run whose suite is still queued is reaped: main alone is faster than the queue"},
		{"spared.has(run.id)", "the spared run has to be excluded from the cancel loop, not only logged"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("cancel-on-merge.yml no longer contains %q — %s", want.needle, want.why)
		}
	}

	if !strings.Contains(src, `workflow_id: "pullfrog.yml"`) {
		t.Error("cancel-on-merge.yml no longer lists the closed PR's Pullfrog reviews, so a review nobody will read keeps its place in the review queue")
	}
}
