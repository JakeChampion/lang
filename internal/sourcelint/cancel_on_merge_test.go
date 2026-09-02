package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reaper in cancel-on-merge.yml decides what to kill by diffing two
// snapshots: the runs in flight on the closed PR's branch, and the head SHAs of
// the PRs still open on that same branch. Both invariants below are orderings
// or spellings inside an inline `github-script` body, so nothing type-checks
// them and a plausible-looking edit can undo either in silence — the failure
// mode is a cancelled run on someone else's live PR, visible only as CI that
// mysteriously never finished.
func TestCancelOnMergeReapsSafely(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cancel-on-merge.yml"))
	if err != nil {
		t.Fatalf("read cancel-on-merge.yml: %v", err)
	}
	src := string(b)

	// The runs have to be collected BEFORE the open PRs. A force-push landing
	// mid-job is routine here (merge main, rebase the sibling branch, push), and
	// in the other order its fresh runs are already listed while the PR still
	// reads at its old head — so they match no open PR and get reaped.
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

	// Every status a run can sit in without having reached a terminal state. A
	// run in a status missing here survives the reap and burns a runner slot
	// testing a branch nobody cares about — the whole point of the workflow.
	for _, status := range []string{"requested", "waiting", "pending", "queued", "in_progress"} {
		if !strings.Contains(src, `"`+status+`"`) {
			t.Errorf("status %q is never swept: runs in it outlive the PR that queued them", status)
		}
	}
}
