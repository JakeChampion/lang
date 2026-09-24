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

// No main lane cancels its own superseded runs.
//
// A lane that cannot finish inside the gap between merges does not report
// LATER, it never reports at all. test-e2e-selfhost is the measurement: it
// needs 29-123 minutes wall-clock (14 of 40 main runs completed over
// 2026-09-03/04), merges here land every two to four minutes, and in the hour
// after it was made to cancel on main, 16 main runs produced zero completions
// — 15 cancelled, most inside three minutes. A lane in that state is green by
// absence, which is the same failure as a lane that never runs, and this
// repository has now paid for it twice in one day. Every lane is now a job of
// ci.yml's one main run, so the run as a whole keeps that rule; the short
// lanes that used to cancel their own main runs have none of their own left.
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
		default:
			if !strings.Contains(setting, "github.ref != 'refs/heads/main'") && setting != "false" {
				t.Errorf("%s cancels its own main runs (`cancel-in-progress: %s`). A lane "+
					"that cannot finish inside the gap between merges does not report "+
					"late — it never reports. Use `false`, or %q where the workflow also "+
					"runs on branches", e.Name(), setting, mainSafeCancel)
			}
		}
	}

	var missing []string
	for name := range actionLanes {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("actionLanes names %v, which no longer exist — a renamed lane leaves "+
			"an entry describing nothing, and the lane itself unclassified", missing)
	}
	if len(checked) == 0 {
		t.Fatal("no main-triggered workflows with a concurrency group — did the " +
			"`on:` or `concurrency:` format change?")
	}
}
