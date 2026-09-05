package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shared idiom: cancel a superseded run on a PR branch, never on main.
const mainSafeCancel = "cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}"

// No workflow triggered by a push to main may auto-cancel its own main runs.
//
// A main run has no successor to inherit its result: cancelling it does not
// defer the work, it drops it. And the lane most likely to be cancelled is the
// one that waits longest for a runner, which here is a pool shared with ~85
// jobs per open PR — so during a busy hour the queue wait exceeds the gap
// between merges and EVERY run is cancelled before it is allocated a machine.
//
// Both halves of that have happened. auto-rebase-prs.yml shipped with
// `cancel-in-progress: true` and its first eleven runs were cancelled with
// runner_id 0 and no steps — it had never executed. pages.yml lost five
// consecutive deploys between 18:47 and 20:32 on 2026-09-05, leaving the docs
// site nearly two hours stale.
//
// Neither is visible: no red check, no failed step, nothing that says the run
// did not happen. The Actions list shows `cancelled`, which in this repository
// is also what a healthy cancel-on-failure reap looks like. Green by absence is
// exactly what a source lint is for.
//
// A workflow with no `concurrency:` at all is not checked — nothing cancels it.
// check-sources.yml is deliberately in that state and documents why.
func TestMainLanesDoNotCancelThemselves(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}

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
		case setting == "":
			// Absent means false, which is what we want, but say so out loud:
			// a reader cannot tell it from an oversight.
			t.Errorf("%s declares a concurrency group with no `cancel-in-progress:`. "+
				"It defaults to false, which is correct for a main lane — write it "+
				"explicitly (`false`, or %q) so the next edit does not read the "+
				"omission as undecided", e.Name(), mainSafeCancel)
		case setting == "false":
		case strings.Contains(setting, "github.ref != 'refs/heads/main'"):
		default:
			t.Errorf("%s cancels its own main runs (`cancel-in-progress: %s`). A main "+
				"run has no successor to inherit its result, and when the queue wait "+
				"exceeds the gap between merges the lane never runs at all. Use %q, or "+
				"`false` if the lane is main-only", e.Name(), setting, mainSafeCancel)
		}
	}

	if len(checked) == 0 {
		t.Fatal("no main-triggered workflows with a concurrency group — did the " +
			"`on:` or `concurrency:` format change?")
	}
}
