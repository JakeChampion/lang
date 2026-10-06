package sourcelint

import (
	"slices"
	"strings"
	"testing"
)

const mainRedFile = "main-red.yml"

// Watch main's entry workflow and old CI runs finishing during the transition.
// Renaming an entry without updating this observer would lose failure reports.
func TestMainRedWatchesCI(t *testing.T) {
	src := workflowSource(t, mainRedFile)
	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", mainRedFile)
	}
	if !strings.Contains(on, "workflow_run:") {
		t.Fatalf("%s no longer triggers on workflow_run", mainRedFile)
	}
	ci, ok := workflowName(workflowSource(t, ciFile))
	if !ok {
		t.Fatalf("%s has no top-level `name:`", ciFile)
	}
	main, ok := workflowName(workflowSource(t, ciMainFile))
	if !ok {
		t.Fatalf("%s has no top-level name", ciMainFile)
	}
	watched := watchedWorkflows(on)
	if len(watched) != 2 || !slices.Contains(watched, ci) || !slices.Contains(watched, main) {
		t.Errorf("%s watches %v; it must watch %q and %q during the main workflow transition", mainRedFile, watched, ci, main)
	}
	self, ok := workflowName(src)
	if !ok {
		t.Fatalf("%s has no top-level `name:`", mainRedFile)
	}
	if self == ci || self == main {
		t.Errorf("%s shares %s's name: it would watch itself and file an issue about "+
			"failing to file an issue", mainRedFile, ciFile)
	}
}

// The reporter must fire for main's PUSHES and nothing else.
//
// A pull_request run is the author's to fix and cancel-on-failure.yml already
// reaps its siblings; filing an issue per red PR lane would fill the tracker
// with work that is already assigned. A workflow_dispatch at a commit is
// someone's deliberate run and may be red on purpose. Both were live mistakes
// to make here, so the guard names the condition rather than trusting it.
func TestMainRedFiresOnMainPushesOnly(t *testing.T) {
	src := workflowSource(t, mainRedFile)

	for _, want := range []string{
		"github.event.workflow_run.event == 'push'",
		"github.event.workflow_run.head_branch == 'main'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%s does not gate on `%s` — it would report on runs that are "+
				"not main's own pushes", mainRedFile, want)
		}
	}

	// The verdict is per LANE, read off the job names of the one run, and both
	// halves are needed: the close half is what stops the tracker filling with
	// issues for lanes that recovered days ago, and the skipped/cancelled
	// guards are what stop a lane the filter turned off from "recovering".
	for _, want := range []struct{ needle, why string }{
		{`lastIndexOf(" / ")`, "a lane is the caller job's name, the part of a job name before the last ` / `"},
		{`j.conclusion === "failure"`, "a lane is red when one of its jobs failed"},
		{`succeeded.length === 0`, "a lane whose jobs all skipped is no verdict, and must not close an issue"},
		{`j.conclusion === "cancelled"`, "a cancelled main job is a person's cancel, not a green"},
		{`lane.startsWith("reap-")`, "the reapers never run on main and must not read as lanes"},
		{`state: "closed"`, "it opens issues it never closes"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", mainRedFile, want.needle, want.why)
		}
	}

	// Identity is the label plus the title. A marker comment would be silently
	// stripped from the issue body — GitHub deletes anything that reads as an
	// HTML tag, and everything up to the next `>` with it — so every run would
	// file a fresh duplicate instead of finding the open one.
	if strings.Contains(src, "!--") {
		t.Errorf("%s looks for an HTML-comment marker in the issue body. GitHub "+
			"strips those, so the lookup finds nothing and each failure opens a "+
			"duplicate. Key the dedupe on the label and title instead.", mainRedFile)
	}
}
