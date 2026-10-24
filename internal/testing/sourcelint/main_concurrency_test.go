package sourcelint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const mainSafeCancel = "cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}"

var actionLanes = map[string]string{
	"auto-rebase-prs.yml": "rebases and pushes every open PR branch",
	"pages.yml":           "builds and deploys the docs site",
	"reap-stale-runs.yml": "cancels the queued runs nobody is waiting on",
}

func TestMainLanesCancelOnlyWhenTheyCanFinish(t *testing.T) {
	dir := filepath.Join("..", "..", "..", ".github", "workflows")
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
			continue
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
