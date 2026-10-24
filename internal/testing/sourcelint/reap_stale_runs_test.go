package sourcelint

import (
	"strings"
	"testing"
)

const reapFile = "reap-stale-runs.yml"

func TestReapStaleRunsCancelsSafely(t *testing.T) {
	src := workflowSource(t, reapFile)

	for _, want := range []struct{ needle, why string }{
		{
			`event: "pull_request"`,
			"a push, release, dispatch or scheduled run exists for a reason that " +
				"has nothing to do with a pull request, and a main run has no " +
				"successor to inherit its result",
		},
		{
			`!live.has(run.head_sha)`,
			"branches are reused by successive pull requests here, so keeping by " +
				"BRANCH spares a dead run and keeping by nothing reaps a live one",
		},
		{
			`String(run.id) === self`,
			"the sweep would cancel itself before it cancelled anything else",
		},
		{
			`e.status === 409`,
			"a run reaching a terminal state between the listing and the cancel is " +
				"routine on a busy queue, not a failure",
		},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", reapFile, want.needle, want.why)
		}
	}

	for _, want := range []struct{ needle, why string }{
		{"adoptedByMain", "nothing spares the run ci.yml's lane selector is waiting for"},
		{"age > 45", "the sparing window must outlast ci.yml's 30-minute wait and then close"},
		{`j.name.startsWith("Full suite / ")`, "a run whose suite is still queued is reaped: main alone is faster"},
		{`workflow_id: "pullfrog.yml"`, "Pullfrog reviews of closed pull requests are not swept"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", reapFile, want.needle, want.why)
		}
	}

	prs := strings.Index(src, "github.rest.pulls.list")
	runs := strings.Index(src, "listWorkflowRunsForRepo")
	switch {
	case prs < 0:
		t.Fatal("no pulls.list call")
	case runs < 0:
		t.Fatal("no listWorkflowRunsForRepo call")
	case prs > runs:
		t.Error("runs are listed before the open pull requests: a PR opened mid-job " +
			"has its fresh runs matching no open head, and they get cancelled")
	}

	for _, status := range []string{"requested", "waiting", "pending", "queued", "in_progress"} {
		if !strings.Contains(src, `"`+status+`"`) {
			t.Errorf("status %q is never swept: runs in it outlive the pull request "+
				"that queued them", status)
		}
	}

	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", reapFile)
	}
	if _, push := triggerBlock(on, "push"); !push {
		if _, pr := triggerBlock(on, "pull_request"); !pr {
			t.Errorf("%s is driven by the clock alone. Its cron did not fire for 69 "+
				"minutes across three slots on the day it landed, because GitHub sheds "+
				"scheduled runs under exactly the load this sweep is for. Keep a "+
				"repository-event trigger (a merge, a closed pull request) as the "+
				"mechanism and the cron as the quiet-hours floor", reapFile)
		}
	}

	if _, ok := topLevelBlock(src, "concurrency"); ok {
		t.Errorf("%s has a concurrency group again. A pending run is not protected by "+
			"`cancel-in-progress: false`: the newest pending run replaces it, so on a "+
			"busy queue the sweep is cancelled before it ever starts. It went eight "+
			"runs without completing once that way", reapFile)
	}
}
