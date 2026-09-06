package sourcelint

import (
	"strings"
	"testing"
)

const reapFile = "reap-stale-runs.yml"

// reap-stale-runs.yml cancels other people's runs on a schedule, so every guard
// deciding WHICH run is a correctness boundary — and all of them live in an
// inline `github-script` body where nothing type-checks them. The failure mode
// is somebody's live CI cancelled from under them, visible only as a check that
// never finished.
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

	// The open pull requests have to be listed BEFORE the runs. In the other
	// order a pull request opened mid-job has runs that were listed while it did
	// not yet exist, so they match no open head and get reaped. Same trap
	// cancel-on-merge.yml documents from the other side.
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

	// Every status a run can sit in without having reached a terminal state. One
	// missing here is a run that survives the sweep and keeps its slot — the
	// whole point of the workflow.
	for _, status := range []string{"requested", "waiting", "pending", "queued", "in_progress"} {
		if !strings.Contains(src, `"`+status+`"`) {
			t.Errorf("status %q is never swept: runs in it outlive the pull request "+
				"that queued them", status)
		}
	}

	// The sweep must be startable by something that actually happens here.
	//
	// It shipped on a `*/20` cron alone and the cron never fired: 69 minutes and
	// three slots after it merged, its only run was a manual dispatch. GitHub's
	// scheduler sheds scheduled runs under load — the same load this sweep
	// exists to relieve — so the one condition that makes it necessary is the
	// condition that stops it starting. A repository event does not have that
	// failure mode, and a merge or a closed pull request is exactly when runs go
	// stale.
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

	// The sweep must carry NO concurrency group.
	//
	// `cancel-in-progress: false` protects the run holding a RUNNER; a run still
	// waiting for one counts as pending, and GitHub keeps only the newest pending
	// run per group. On a queue this deep nothing reaches a runner before the next
	// merge arrives, so a group does not bound the sweep's cost — it guarantees it
	// never finishes. That is what happened: with a group, runs 3 through 8 were
	// each cancelled within seconds of the next trigger, having done nothing at
	// all, and the workflow that exists to drain the queue had never once drained
	// it.
	//
	// A slot per merge is affordable here for the same reason check-sources.yml
	// gives for going without one: the job is seconds of API calls. Two sweeps
	// overlapping is harmless — both list the open pull requests before the runs,
	// and a cancel the other already made returns 409, which is handled above.
	if _, ok := topLevelBlock(src, "concurrency"); ok {
		t.Errorf("%s has a concurrency group again. A pending run is not protected by "+
			"`cancel-in-progress: false`: the newest pending run replaces it, so on a "+
			"busy queue the sweep is cancelled before it ever starts. It went eight "+
			"runs without completing once that way", reapFile)
	}
}
