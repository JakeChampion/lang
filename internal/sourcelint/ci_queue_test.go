package sourcelint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The CI admission queue (docs/CI-QUEUE.md) is spread across three places that
// have to agree: the lane manifest (.github/queue/lanes.json), the lane
// workflows themselves, and the `workflow_run` trigger lists that wake the
// queue and the PR status comment. Nothing in GitHub Actions checks that they
// do — a lane added to the manifest but missing `workflow_dispatch` simply
// never runs, and a heavy workflow left off the manifest quietly goes back to
// running for every PR at once. These guards are that check.
//
// They are pure text scans: the module has no dependencies and this file is
// not the place to acquire a YAML parser. The workflows are hand-written with
// a uniform shape, so anchored line matching is enough — and a reshuffle that
// breaks the scan fails loudly here rather than silently in CI.

const (
	lanesManifest      = ".github/queue/lanes.json"
	queueWorkflow      = ".github/workflows/ci-queue.yml"
	statusWorkflow     = ".github/workflows/pr-status-comment.yml"
	queueTests         = ".github/queue/queue.test.js"
	workflowDir        = ".github/workflows"
	forkOnlyGuardStart = "if: >-"
)

// forkOnlyGuard is the admission guard every lane's `gate` job carries: run on
// `pull_request` only when the PR comes from a fork, because same-repo PRs are
// dispatched by the queue instead.
var forkOnlyGuard = []string{
	"github.event_name != 'pull_request'",
	"|| github.event.pull_request.head.repo.full_name != github.repository",
}

type lane struct {
	File string `json:"file"`
	Name string `json:"name"`
}

func loadLanes(t *testing.T, root string) []lane {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, lanesManifest))
	if err != nil {
		t.Fatalf("read %s: %v", lanesManifest, err)
	}
	var manifest struct {
		Lanes []lane `json:"lanes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse %s: %v", lanesManifest, err)
	}
	if len(manifest.Lanes) == 0 {
		t.Fatalf("%s lists no lanes", lanesManifest)
	}
	return manifest.Lanes
}

func readWorkflow(t *testing.T, root, file string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, workflowDir, file))
	if err != nil {
		t.Fatalf("read workflow %s: %v", file, err)
	}
	return string(raw)
}

// onBlock returns the lines of a workflow's top-level `on:` mapping, comments
// stripped. It ends at the next column-0 key.
func onBlock(src string) []string {
	var block []string
	in := false
	for _, line := range strings.Split(src, "\n") {
		if !in {
			in = line == "on:"
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			block = append(block, line)
		}
	}
	return block
}

// triggers reports the trigger names in an `on:` block mapped to whether the
// trigger carries any sub-configuration (`types:`, `paths:`, ...).
func triggers(block []string) map[string]bool {
	out := map[string]bool{}
	var last string
	for _, line := range block {
		switch {
		case strings.HasPrefix(line, "    ") && last != "":
			out[last] = true
		case strings.HasPrefix(line, "  "):
			last = strings.TrimSuffix(strings.TrimSpace(line), ":")
			if _, seen := out[last]; !seen {
				out[last] = false
			}
		}
	}
	return out
}

// workflowRunList returns the `workflows:` names of a `workflow_run` trigger.
func workflowRunList(t *testing.T, src string) []string {
	t.Helper()
	var names []string
	in := false
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "workflows:" {
			in = true
			continue
		}
		if !in {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		names = append(names, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
	}
	return names
}

func workflowName(src string) string {
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "name: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
	}
	return ""
}

// TestCIQueueLanesAreDispatchable pins the contract every queued lane has to
// satisfy for the queue to be able to run it: the file exists, its `name:`
// matches the manifest (that name is how the queue's `workflow_run` trigger
// finds it), it accepts `workflow_dispatch` (how the queue starts it), and its
// gate job carries the fork-only admission guard (what stops it running for
// every same-repo PR at once). It also rejects a path-filtered workflow as a
// lane: `paths:` does not apply to `workflow_dispatch`, so queueing one would
// make it run on EVERY front-of-queue PR instead of only the relevant ones.
func TestCIQueueLanesAreDispatchable(t *testing.T) {
	root := moduleRoot(t)
	for _, l := range loadLanes(t, root) {
		src := readWorkflow(t, root, l.File)
		if got := workflowName(src); got != l.Name {
			t.Errorf("%s: workflow name is %q, manifest says %q", l.File, got, l.Name)
		}
		trig := triggers(onBlock(src))
		if _, ok := trig["workflow_dispatch"]; !ok {
			t.Errorf("%s: no `workflow_dispatch` trigger — the queue cannot start this lane", l.File)
		}
		if configured, ok := trig["pull_request"]; ok && configured {
			t.Errorf("%s: queued lanes take a bare `pull_request:` (the fork fallback); "+
				"a filtered one would behave differently under dispatch, where `paths:`/"+
				"`types:` do not apply", l.File)
		}
		for _, want := range forkOnlyGuard {
			if !strings.Contains(src, want) {
				t.Errorf("%s: gate job is missing the fork-only admission guard %q — this lane "+
					"would run for every same-repo PR, bypassing the queue (docs/CI-QUEUE.md)",
					l.File, want)
			}
		}
	}
}

// TestCIQueueTriggerListsMatchManifest keeps the two hand-maintained
// `workflow_run` lists honest. The queue's list is what wakes it when runners
// free up; a lane missing from it stalls the queue until the scheduled sweep.
// The status comment's list is what re-evaluates a PR's rollup.
func TestCIQueueTriggerListsMatchManifest(t *testing.T) {
	root := moduleRoot(t)
	lanes := loadLanes(t, root)

	want := map[string]bool{}
	for _, l := range lanes {
		want[l.Name] = true
	}

	got := map[string]bool{}
	for _, n := range workflowRunList(t, readWorkflow(t, root, filepath.Base(queueWorkflow))) {
		got[n] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("ci-queue.yml: %q is a lane but is not in its `workflow_run` list — the queue "+
				"would not notice that lane finishing", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("ci-queue.yml: `workflow_run` lists %q, which is not a lane in %s", name, lanesManifest)
		}
	}

	watched := map[string]bool{}
	for _, n := range workflowRunList(t, readWorkflow(t, root, filepath.Base(statusWorkflow))) {
		watched[n] = true
	}
	for name := range want {
		if !watched[name] {
			t.Errorf("pr-status-comment.yml: %q is not in its `workflow_run` list, so the PR rollup "+
				"never re-evaluates when that lane finishes", name)
		}
	}
}

// TestCIQueueCoversEveryHeavyLane is the forcing function the manifest needs: a
// new workflow that runs unconditionally on every pull request has to either
// join the queue or explicitly opt out here. Without it, adding a workflow
// silently re-creates the everything-at-once pile-up the queue exists to stop.
func TestCIQueueCoversEveryHeavyLane(t *testing.T) {
	root := moduleRoot(t)

	// Workflows that legitimately run on every PR outside the queue.
	exempt := map[string]string{
		"lint.yml":     "the fast lane — immediate feedback is the point (docs/CI-QUEUE.md)",
		"ci-queue.yml": "the queue itself",
	}
	queued := map[string]bool{}
	for _, l := range loadLanes(t, root) {
		if reason, ok := exempt[l.File]; ok {
			t.Errorf("%s is queued but exempt (%s) — pick one", l.File, reason)
		}
		queued[l.File] = true
	}

	entries, err := os.ReadDir(filepath.Join(root, workflowDir))
	if err != nil {
		t.Fatalf("read %s: %v", workflowDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		trig := triggers(onBlock(readWorkflow(t, root, e.Name())))
		configured, onPR := trig["pull_request"]
		// A configured `pull_request:` (`paths:` / `types:`) is already
		// selective, so it is not part of the every-PR pile-up.
		if !onPR || configured || queued[e.Name()] {
			continue
		}
		if _, ok := exempt[e.Name()]; ok {
			continue
		}
		t.Errorf("%s runs on every pull request but is not in %s and is not exempt: add it to the "+
			"queue, give its trigger a `paths:`/`types:` filter, or record why it stays unqueued",
			e.Name(), lanesManifest)
	}
}

// TestCIQueueDecider runs the queue's own unit tests (node --test). The
// decision logic — FIFO order, busy detection, which runs count as "this
// commit has had CI" — lives in JavaScript because that is what
// actions/github-script executes; running it from the Go suite means it is
// covered by the same `go test` everything else is.
func TestCIQueueDecider(t *testing.T) {
	root := moduleRoot(t)
	node, err := exec.LookPath("node")
	if err != nil {
		// Node is preinstalled on every GitHub-hosted runner, so on CI a
		// missing node is a broken image, not an excused skip.
		if os.Getenv("GITHUB_ACTIONS") != "" {
			t.Fatalf("node not found on a CI runner: %v", err)
		}
		t.Skipf("node not installed; skipping %s (install node to run it locally)", queueTests)
	}
	cmd := exec.Command(node, "--test", queueTests)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node --test %s failed: %v\n%s", queueTests, err, out)
	}
}
