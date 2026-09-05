package sourcelint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The idiom that supersedes on a branch and never on main.
const mainSafeCancel = "cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}"

// actionLanes are the main lanes whose run DOES something rather than reporting
// something. A cancelled test run costs a result the next run reproduces; a
// cancelled action is work that silently never happens, and main runs have no
// successor to inherit it.
//
// Both entries have already failed that way. auto-rebase-prs.yml shipped with
// `cancel-in-progress: true` and its first eleven runs were cancelled with
// runner_id 0 and no steps — it had never executed once. pages.yml lost five
// consecutive deploys between 18:47 and 20:32 on 2026-09-05, leaving the docs
// site nearly two hours stale.
//
// Adding a lane that pushes, deploys, publishes or comments? It belongs here.
var actionLanes = map[string]string{
	"auto-rebase-prs.yml": "rebases and pushes every open PR branch",
	"pages.yml":           "builds and deploys the docs site",
	"reap-stale-runs.yml": "cancels the queued runs nobody is waiting on",
}

// cancelOnMainLanes are the lanes short enough to finish between two merges, so
// superseding an in-flight main run costs a result the next one reproduces
// within minutes — and hands its slot back, which is what the pool needs.
//
// It is an ALLOWLIST, and that direction is the point: a lane not named here
// keeps its main runs. The safe behaviour is what a new lane gets by default,
// because the failure of the other default is invisible.
//
// Measured or documented, per lane: lint ~5 min of work; the fuzz targets 60
// seconds each; fernsmith ~60 s; perf's `bench` is seconds of measurement on a
// Go build, and its self-host job builds once for three sub-minute measures;
// docs-build, vscode-extension and playground-e2e carry 10-15 minute timeouts
// on jobs that do far less.
var cancelOnMainLanes = map[string]bool{
	"lint.yml":             true,
	"fuzz-diff.yml":        true,
	"test-fernsmith.yml":   true,
	"perf.yml":             true,
	"docs-build.yml":       true,
	"vscode-extension.yml": true,
	"playground-e2e.yml":   true,
}

// A main lane cancels its own superseded runs only when it can finish between
// merges. Everything else keeps them.
//
// The queue is the scarce resource — 75% of a PR's CI time is spent waiting
// rather than computing (docs/CI-SIGNOFF.md) — so a burst of merges should not
// hold a runner for every result the newest commit supersedes. That is why the
// short lanes cancel.
//
// It stops being a saving the moment a lane cannot finish inside the gap
// between merges, because then it does not report LATER, it never reports at
// all. test-e2e-selfhost is the measurement: it needs 29-123 minutes wall-clock
// (14 of 40 main runs completed over 2026-09-03/04), merges here land every two
// to four minutes, and in the hour after it was made to cancel on main, 16 main
// runs produced zero completions — 15 cancelled, most inside three minutes. A
// lane in that state is green by absence, which is the same failure as a lane
// that never runs, and this repository has now paid for it twice in one day.
//
// check-sources.yml is not checked here: it carries no concurrency group at all,
// deliberately, to keep per-merge attribution on a one-minute job.
func TestMainLanesCancelOnlyWhenTheyCanFinish(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}

	seen := map[string]bool{}
	var checked []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(b)
		seen[e.Name()] = true

		on, ok := onBlock(src)
		if !ok {
			continue
		}
		push, ok := triggerBlock(on, "push")
		if !ok || !strings.Contains(push, "branches: [main]") {
			continue
		}
		block, ok := topLevelBlock(src, "concurrency")
		if !ok {
			continue // nothing cancels it
		}
		checked = append(checked, e.Name())

		var setting string
		for _, l := range strings.Split(block, "\n") {
			if s, ok := strings.CutPrefix(strings.TrimSpace(l), "cancel-in-progress:"); ok {
				setting = strings.TrimSpace(s)
			}
		}

		switch {
		case actionLanes[e.Name()] != "":
			if setting != "false" {
				t.Errorf("%s %s, so a cancelled run is work that never happens — main "+
					"has no later run to inherit it. It must be "+
					"`cancel-in-progress: false`, not %q",
					e.Name(), actionLanes[e.Name()], setting)
			}
		case cancelOnMainLanes[e.Name()]:
			if setting != "true" {
				t.Errorf("%s is listed as short enough to finish between merges but does "+
					"not cancel its superseded main runs (%q). Either cancel, or drop it "+
					"from cancelOnMainLanes", e.Name(), setting)
			}
		default:
			if !strings.Contains(setting, "github.ref != 'refs/heads/main'") && setting != "false" {
				t.Errorf("%s cancels its own main runs (`cancel-in-progress: %s`) and is "+
					"not listed as short enough to finish between merges. A lane that "+
					"cannot finish inside the gap between merges does not report late — "+
					"it never reports. Use %q, or add it to cancelOnMainLanes with the "+
					"measurement that says it finishes in minutes",
					e.Name(), setting, mainSafeCancel)
			}
		}
	}

	var missing []string
	for name := range actionLanes {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range cancelOnMainLanes {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these lists name %v, which no longer exist — a renamed lane leaves "+
			"an entry describing nothing, and the lane itself unclassified", missing)
	}
	if len(checked) == 0 {
		t.Fatal("no main-triggered workflows with a concurrency group — did the " +
			"`on:` or `concurrency:` format change?")
	}
}
