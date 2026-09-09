package sourcelint

import (
	"strings"
	"testing"
)

const mainRedFile = "main-red.yml"

// main-red.yml watches the one workflow a push to main launches — ci.yml, the
// same run that gates a pull request — and reads the lanes off its job names.
// There is no per-lane list left to drift, but the one entry that remains can:
// a renamed orchestrator leaves a `workflows:` filter matching nothing, and a
// red main reported by nothing at all is exactly how nine lanes sat red across
// two PRs before anyone read them.
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
	watched := watchedWorkflows(on)
	if len(watched) != 1 || watched[0] != ci {
		t.Errorf("%s watches %v; it must watch exactly %q, the one workflow a push to "+
			"main launches — every lane is a job of that run", mainRedFile, watched, ci)
	}
	self, ok := workflowName(src)
	if !ok {
		t.Fatalf("%s has no top-level `name:`", mainRedFile)
	}
	if self == ci {
		t.Errorf("%s shares %s's name: it would watch itself and file an issue about "+
			"failing to file an issue", mainRedFile, ciFile)
	}
}

// The reporter must fire for main's PUSHES and nothing else.
//
// A pull_request run is the author's to fix and cancel-on-failure.yml already
// reaps its siblings; filing an issue per red PR lane would bury the tracker
// under work that is already assigned. A workflow_dispatch at a commit is
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
