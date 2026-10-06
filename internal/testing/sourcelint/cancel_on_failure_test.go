package sourcelint

import (
	"sort"
	"strings"
	"testing"
)

// workflowName returns a workflow's top-level `name:`.
func workflowName(src string) (string, bool) {
	for _, l := range strings.Split(src, "\n") {
		if rest, ok := strings.CutPrefix(l, "name: "); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// watchedWorkflows returns the `workflows:` filter of a `workflow_run`
// trigger.
func watchedWorkflows(on string) []string {
	var out []string
	inList := false
	for _, l := range strings.Split(on, "\n") {
		trimmed := strings.TrimSpace(l)
		switch {
		case trimmed == "workflows:":
			inList = true
		case inList && strings.HasPrefix(trimmed, `- "`):
			out = append(out, strings.Trim(strings.TrimPrefix(trimmed, "- "), `"`))
		case inList && trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			inList = false
		}
	}
	return out
}

// ci.yml calls the reaper once per lane, from a job that waits on that lane
// alone. That is what cancels the run the moment ANY lane concludes failure
// rather than when all of them have — and every lane needs its own, because a
// job can wait on all of a set or on one of it, never on the first of it.
//
// A lane missing here is silent in both directions: its own failure reaps
// nothing, and it holds the PR-wide lock while a sibling is already red. That
// is the shape of drift this repo has paid for repeatedly — a list maintained
// by hand beside the thing it describes (scripts/unit-test-packages, and
// TestPRLanesShareOneDocOnlyFilter next door).
func TestCancelOnFailureWatchesEveryPRLane(t *testing.T) {
	lanes := ciLanes(t)
	reapers := map[string]ciJob{}
	for _, j := range ciJobs(t) {
		if j.usesFile() == reaperFile {
			reapers[j.id] = j
		}
	}

	for id, lane := range lanes {
		r, ok := reapers["reap-"+id]
		if !ok {
			t.Errorf("%s: lane %q has no `reap-%s` job calling %s — its failure would cancel "+
				"nothing, and it would hold the lock while a sibling is red", ciFile, id, id, reaperFile)
			continue
		}
		delete(reapers, "reap-"+id)
		if r.keys["needs"] != "["+id+"]" {
			t.Errorf("%s: reap-%s needs %q, not `[%s]` — it must wait on that one lane alone, "+
				"or it fires when the wrong lane fails, or only when all of them have finished",
				ciFile, id, r.keys["needs"], id)
		}
		cond := r.keys["if"]
		for _, want := range []struct{ needle, why string }{
			{"!cancelled()", "without a status function in the condition `success()` is implied, " +
				"and a job that waits on a FAILED one never runs at all"},
			{"needs." + id + ".result == 'failure'", "it must fire on this lane's failure, not " +
				"on `failure()`, which is also true when the `changes` job upstream failed"},
			{"github.event_name == 'pull_request'", "a red main must run to completion so " +
				"main-red.yml's failing-job list is the whole set, and a dispatch is " +
				"someone's deliberate run"},
		} {
			if !strings.Contains(cond, want.needle) {
				t.Errorf("%s: reap-%s's `if:` lacks %q — %s", ciFile, id, want.needle, want.why)
			}
		}
		if got, want := r.with["lane"], lane.keys["name"]; got != want {
			t.Errorf("%s: reap-%s names the lane %q; the lane job is named %q", ciFile, id, got, want)
		}
		if r.perms["actions"] != "write" {
			t.Errorf("%s: reap-%s does not pass `actions: write` — the called workflow can only "+
				"narrow what it is handed, and cancelling a run needs it", ciFile, id)
		}
	}

	var extra []string
	for id := range reapers {
		extra = append(extra, id)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("%s: %v call %s but wait on no lane — a renamed or deleted lane leaves "+
			"a reaper describing nothing", ciFile, extra, reaperFile)
	}
}

// The reaper cancels the run it is part of, so every guard that decides
// WHETHER is a correctness boundary — and all of them live in an inline
// `github-script` body where nothing type-checks them. The failure mode is a
// cancelled run somebody was relying on, visible only as CI that never
// finished. Same reasoning as TestCancelOnMergeReapsSafely next door.
func TestCancelOnFailureReapsSafely(t *testing.T) {
	src := workflowSource(t, reaperFile)

	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", reaperFile)
	}
	if !strings.Contains(on, "workflow_call:") {
		t.Fatalf("%s is no longer a `workflow_call` — %s cannot call it per lane", reaperFile, ciFile)
	}
	for _, trigger := range []string{"pull_request", "push", "workflow_run"} {
		if _, ok := triggerBlock(on, trigger); ok {
			t.Errorf("%s triggers on %s of its own accord; it must run only where %s puts it, "+
				"after one lane's failure", reaperFile, trigger, ciFile)
		}
	}

	for _, want := range []struct{ needle, why string }{
		{
			`run_id: context.runId`,
			"it must cancel the run it is IN — the sibling lanes of the one that failed — " +
				"and no other",
		},
		{
			`listPullRequestsAssociatedWithCommit`,
			"the ci-full label is read from the commit so a label added after the run " +
				"started still counts, and a fork PR is found too",
		},
		{
			`l.name === "ci-full"`,
			"the label is the only way to get a full picture of a round's failures",
		},
		{
			`e.status === 409`,
			"a run reaching a terminal state between the decision and the cancel is " +
				"routine, not a failure",
		},
		{
			`e.status === 403`,
			"a fork pull request gets a read-only token; the lanes finishing is not " +
				"the red lane's failure",
		},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", reaperFile, want.needle, want.why)
		}
	}
}
