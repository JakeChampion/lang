package sourcelint

import (
	"slices"
	"strings"
	"testing"
)

const mainRedFile = "main-red.yml"

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

	if strings.Contains(src, "!--") {
		t.Errorf("%s looks for an HTML-comment marker in the issue body. GitHub "+
			"strips those, so the lookup finds nothing and each failure opens a "+
			"duplicate. Key the dedupe on the label and title instead.", mainRedFile)
	}
}
