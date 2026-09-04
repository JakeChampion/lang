package sourcelint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const cancelOnFailureFile = "cancel-on-failure.yml"

// prLaneNames returns the `name:` of every workflow a pull request launches —
// the lanes cancel-on-failure.yml has to watch. `types: [closed]` lanes are
// lifecycle hooks that launch no suite, and the reaper itself is excluded by
// construction: it is not triggered by `pull_request` at all.
func prLaneNames(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}
	names := map[string]string{}
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
			continue
		}
		if strings.Contains(on, "types: [closed]") {
			continue
		}
		name, ok := workflowName(src)
		if !ok {
			t.Fatalf("%s has no top-level `name:`", e.Name())
		}
		names[name] = e.Name()
	}
	if len(names) == 0 {
		t.Fatal("no pull_request workflows found — did the `on:` format change?")
	}
	return names
}

func workflowName(src string) (string, bool) {
	for _, l := range strings.Split(src, "\n") {
		if rest, ok := strings.CutPrefix(l, "name: "); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// watchedWorkflows returns the `workflows:` filter of cancel-on-failure.yml's
// `workflow_run` trigger.
func watchedWorkflows(t *testing.T, on string) []string {
	t.Helper()
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

// The reaper's `workflows:` filter names every lane a pull request launches.
//
// GitHub matches that filter by workflow name, so it cannot be derived at run
// time, and a lane missing from it is silent in both directions: the lane's own
// failure reaps nothing, and it keeps its runner slot when a sibling goes red.
// That is the shape of drift this repo has paid for repeatedly — a list
// maintained by hand beside the thing it describes (scripts/unit-test-packages,
// and TestPRWorkflowsShareOneDocOnlyFilter next door).
func TestCancelOnFailureWatchesEveryPRLane(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	lanes := prLaneNames(t, dir)

	b, err := os.ReadFile(filepath.Join(dir, cancelOnFailureFile))
	if err != nil {
		t.Fatalf("read %s: %v", cancelOnFailureFile, err)
	}
	src := string(b)
	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", cancelOnFailureFile)
	}
	if !strings.Contains(on, "workflow_run:") {
		t.Fatalf("%s no longer triggers on workflow_run", cancelOnFailureFile)
	}

	watched := map[string]bool{}
	for _, n := range watchedWorkflows(t, on) {
		if watched[n] {
			t.Errorf("%q is listed twice", n)
		}
		watched[n] = true
	}

	for name, file := range lanes {
		if !watched[name] {
			t.Errorf("%s runs on pull requests as %q but %s does not watch it: its "+
				"failure would cancel nothing, and it would survive every sibling's",
				file, name, cancelOnFailureFile)
		}
	}
	var extra []string
	for name := range watched {
		if _, ok := lanes[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("%s watches %v, which no longer run on pull requests — a renamed or "+
			"deleted lane leaves a filter entry matching nothing",
			cancelOnFailureFile, extra)
	}

	self, ok := workflowName(src)
	if !ok {
		t.Fatalf("%s has no top-level `name:`", cancelOnFailureFile)
	}
	if watched[self] {
		t.Errorf("%s watches itself (%q): a reaper that concludes `failure` would "+
			"trigger another reaper", cancelOnFailureFile, self)
	}
}

// The reaper cancels runs, so every guard that decides WHICH runs is a
// correctness boundary — and all of them live in an inline `github-script`
// body or a YAML `if:`, where nothing type-checks them. The failure mode is a
// cancelled run somebody was relying on, visible only as CI that never
// finished. Same reasoning as TestCancelOnMergeReapsSafely next door.
func TestCancelOnFailureReapsSafely(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", cancelOnFailureFile))
	if err != nil {
		t.Fatalf("read %s: %v", cancelOnFailureFile, err)
	}
	src := string(b)

	for _, want := range []struct{ needle, why string }{
		{
			`github.event.workflow_run.conclusion == 'failure'`,
			"without it a green lane reaps its siblings",
		},
		{
			`github.event.workflow_run.event == 'pull_request'`,
			"a red main must run to completion to stay attributable, and a " +
				"workflow_dispatch at that commit is someone's deliberate run",
		},
		{
			`head_sha: sha`,
			"scoping by branch instead of commit reaps the runs a force-push just " +
				"started, and successive PRs share a branch name here",
		},
		{
			`run.event !== "pull_request"`,
			"a release, a dispatch or a reaper at the same commit was started for a " +
				"reason that has nothing to do with this pull request being red",
		},
		{
			`e.status === 409`,
			"a run reaching a terminal state between the listing and the cancel is " +
				"routine, not a failure",
		},
		{
			`l.name === "ci-full"`,
			"the label is the only way to get a full picture of a round's failures",
		},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", cancelOnFailureFile, want.needle, want.why)
		}
	}

	// Every status a run can sit in without having reached a terminal state. One
	// missing here survives the reap and holds a slot for a pull request that is
	// already red — the whole point of the workflow.
	for _, status := range []string{"requested", "waiting", "pending", "queued", "in_progress"} {
		if !strings.Contains(src, `"`+status+`"`) {
			t.Errorf("status %q is never swept: runs in it outlive the failure that "+
				"made them pointless", status)
		}
	}
}
