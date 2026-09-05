package sourcelint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// actionLanes are the main lanes whose run DOES something rather than reporting
// something. A cancelled test run costs a result that the next run reproduces;
// a cancelled action is work that silently never happens, and main runs have no
// successor to inherit it.
//
// Both entries here have already failed that way. auto-rebase-prs.yml shipped
// with `cancel-in-progress: true` and its first eleven runs were cancelled with
// runner_id 0 and no steps — it had never executed once. pages.yml lost five
// consecutive deploys between 18:47 and 20:32 on 2026-09-05, leaving the docs
// site nearly two hours stale.
//
// Adding a lane that pushes, deploys, publishes or comments? It belongs here.
var actionLanes = map[string]string{
	"auto-rebase-prs.yml": "rebases and pushes every open PR branch",
	"pages.yml":           "builds and deploys the docs site",
}

// Main lanes cancel their own superseded runs; action lanes do not.
//
// The queue here is the scarce resource — 75% of a PR's CI time is spent
// waiting, not computing (docs/CI-SIGNOFF.md) — and when several merges land in
// a burst, only the newest main commit's result is worth the slots. Cancelling
// the older main runs hands those slots back.
//
// What that trades away is per-merge attribution: a red main names the newest
// commit of the burst rather than the merge that caused it. check-sources.yml
// keeps that attribution deliberately by carrying no concurrency group at all,
// which is why it is not checked here — nothing cancels it.
func TestMainLanesCancelExceptWhenTheyAct(t *testing.T) {
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

		if why, isAction := actionLanes[e.Name()]; isAction {
			if setting != "false" {
				t.Errorf("%s %s, so a cancelled run is work that never happens — main "+
					"has no later run to inherit it. It must be "+
					"`cancel-in-progress: false`, not %q", e.Name(), why, setting)
			}
			continue
		}
		if setting != "true" {
			t.Errorf("%s does not cancel its superseded main runs (`cancel-in-progress: "+
				"%s`). A burst of merges then holds a runner each for results only the "+
				"newest commit's supersedes. Use `true`, or add the lane to actionLanes "+
				"if its run does something a later run cannot redo", e.Name(), setting)
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
			"an entry protecting nothing, and the lane itself unprotected", missing)
	}
	if len(checked) == 0 {
		t.Fatal("no main-triggered workflows with a concurrency group — did the " +
			"`on:` or `concurrency:` format change?")
	}
}
