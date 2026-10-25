package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sweep is a composite action. reap-stale-runs.yml runs it on the cron as
// the quiet-hours floor; main-housekeeping.yml (every push to main) and
// cancel-on-merge.yml (every closed pull request) run it on the events.
const reapFile = "reap-stale-runs.yml"

func TestReapStaleRunsCancelsSafely(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actions", "reap-stale-runs", "action.yml"))
	if err != nil {
		t.Fatalf("read the reap action: %v", err)
	}
	src := string(b)

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

	// The clock alone never carried the sweep: its cron did not fire for 69
	// minutes across three slots on the day it landed, because GitHub sheds
	// scheduled runs under exactly the load the sweep is for. A merge and a
	// closed pull request are the events, each in the job that push already
	// runs.
	for file, event := range map[string]string{"main-housekeeping.yml": "push", "cancel-on-merge.yml": "pull_request"} {
		wf := workflowSource(t, file)
		if !strings.Contains(wf, "uses: ./.github/actions/reap-stale-runs") {
			t.Errorf("%s does not run the reap action; the sweep loses its %s trigger", file, event)
		}
		on, ok := onBlock(wf)
		if !ok {
			t.Fatalf("%s has no `on:` block", file)
		}
		if _, ok := triggerBlock(on, event); !ok {
			t.Errorf("%s no longer runs on %s", file, event)
		}
	}

	floor := workflowSource(t, reapFile)
	if !strings.Contains(floor, "uses: ./.github/actions/reap-stale-runs") || !strings.Contains(floor, "cron:") {
		t.Errorf("%s is not the cron floor of the reap action", reapFile)
	}
	for _, file := range []string{reapFile, "main-housekeeping.yml"} {
		if _, ok := topLevelBlock(workflowSource(t, file), "concurrency"); ok {
			t.Errorf("%s has a concurrency group. A pending run is not protected by "+
				"`cancel-in-progress: false`: the newest pending run replaces it, so on a "+
				"busy queue the sweep is cancelled before it ever starts. It went eight "+
				"runs without completing once that way", file)
		}
	}
}
