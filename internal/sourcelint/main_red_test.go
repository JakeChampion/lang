package sourcelint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const mainRedFile = "main-red.yml"

// main-red.yml watches the same lanes cancel-on-failure.yml does, and for the
// same reason in the other direction: the lanes that gate a pull request are
// the lanes that gate main (TestGateLanesRunOnMain mirrors every one onto a
// push). A lane missing from this list is one whose red main is reported by
// nothing at all — which is the state the whole file exists to end, and is
// exactly how nine lanes sat red across two PRs before anyone read them.
//
// Same drift shape as its sibling, so it gets the same guard: a hand-kept list
// of a directory that grows elsewhere. Adding a lane and forgetting this entry
// costs silence, and silence here is indistinguishable from green.
func TestMainRedWatchesEveryMainLane(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	lanes := prLaneNames(t, dir)

	b, err := os.ReadFile(filepath.Join(dir, mainRedFile))
	if err != nil {
		t.Fatalf("read %s: %v", mainRedFile, err)
	}
	src := string(b)
	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", mainRedFile)
	}
	if !strings.Contains(on, "workflow_run:") {
		t.Fatalf("%s no longer triggers on workflow_run", mainRedFile)
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
			t.Errorf("%s gates main as %q but %s does not watch it: its failure on "+
				"main would open no issue and be reported by nothing",
				file, name, mainRedFile)
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
		t.Errorf("%s watches %v, which no longer gate main — a renamed or deleted "+
			"lane leaves a filter entry matching nothing", mainRedFile, extra)
	}

	self, ok := workflowName(src)
	if !ok {
		t.Fatalf("%s has no top-level `name:`", mainRedFile)
	}
	if watched[self] {
		t.Errorf("%s watches itself (%q): a reporter that concludes `failure` would "+
			"file an issue about having failed to file an issue", mainRedFile, self)
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
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", mainRedFile))
	if err != nil {
		t.Fatalf("read %s: %v", mainRedFile, err)
	}
	src := string(b)

	for _, want := range []string{
		"github.event.workflow_run.event == 'push'",
		"github.event.workflow_run.head_branch == 'main'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%s does not gate on `%s` — it would report on runs that are "+
				"not main's own pushes", mainRedFile, want)
		}
	}

	// Both conclusions, because the close half is what stops the tracker
	// filling with issues for lanes that recovered days ago.
	for _, want := range []string{
		"conclusion == 'failure'",
		"conclusion == 'success'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%s does not act on `%s`: it opens issues it never closes, or "+
				"closes issues it never opens", mainRedFile, want)
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
