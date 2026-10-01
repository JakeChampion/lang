package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reaper in cancel-on-merge.yml decides what to cancel by diffing two
// snapshots: the runs in flight on the closed PR's branch, and the head SHAs of
// the PRs still open on that same branch. Both invariants below are orderings
// or spellings inside an inline `github-script` body, so nothing type-checks
// them and a plausible-looking edit can undo either in silence — the failure
// mode is a cancelled run on someone else's live PR, visible only as CI that
// never finished.
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
	// run in a status missing here survives the reap and wastes a runner slot
	// testing a branch nobody cares about — the whole point of the workflow.
	for _, status := range []string{"requested", "waiting", "pending", "queued", "in_progress"} {
		if !strings.Contains(src, `"`+status+`"`) {
			t.Errorf("status %q is never swept: runs in it outlive the PR that queued them", status)
		}
	}

	// The one run on a merged PR's branch that is NOT reaped: its CI run at the
	// merged head, when the merge pushed that exact tree and the run's suite is
	// under way. ci.yml's lane selector waits for that run on the main push and
	// skips every lane it passes, so reaping it hands main the whole suite from
	// zero. The three conditions are each a needle, because dropping any one
	// either reaps main's evidence or spares a run testing a tree main never got.
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

	// The closed PR's queued Pullfrog reviews go with it: they are dispatched on
	// main and named for the PR, so the branch listing never sees them.
	if !strings.Contains(src, `workflow_id: "pullfrog.yml"`) {
		t.Error("cancel-on-merge.yml no longer lists the closed PR's Pullfrog reviews, so a review nobody will read keeps its place in the review queue")
	}
}
