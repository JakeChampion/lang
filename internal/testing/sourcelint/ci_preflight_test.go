package sourcelint

import (
	"strings"
	"testing"
)

func TestCILintRunsOutsideTheFullSuiteQueue(t *testing.T) {
	conc, ok := topLevelBlock(workflowSource(t, ciFile), "concurrency")
	if !ok {
		t.Fatal("root CI has no concurrency group: a superseded PR run would hold runner slots until a reaper reaches it")
	}
	if !strings.Contains(conc, "github.event.pull_request.number") || !strings.Contains(conc, "github.run_id") {
		t.Errorf("root CI's group must be per pull request, with a run-unique fallback for main and dispatch: %q", conc)
	}
	if !strings.Contains(conc, "cancel-in-progress: ${{ github.event_name == 'pull_request' }}") {
		t.Errorf("root CI must cancel only a pull request's superseded run, never a main validation: %q", conc)
	}
	if strings.Contains(conc, "ci-run-") {
		t.Errorf("root CI's fallback group shares %s's `ci-run-` prefix; the two collide inside one main validation", ciSuiteFile)
	}
	if strings.Contains(conc, "queue:") {
		t.Errorf("root CI must not queue lint behind a full compiler suite; the suite FIFO belongs to %s", ciSuiteFile)
	}
	rootJobs := workflowJobs(t, ciFile)
	allowed := map[string]bool{"changes": true, "lint": true, "preflight": true, "suite": true, "reap-lint": true, "reap-preflight": true}
	var suite, preflight ciJob
	for _, job := range rootJobs {
		if !allowed[job.id] {
			t.Errorf("unexpected root job %s bypasses the full-suite queue", job.id)
		}
		delete(allowed, job.id)
		switch job.id {
		case "suite":
			suite = job
		case "preflight":
			preflight = job
		}
	}
	if len(allowed) != 0 {
		t.Fatalf("missing preflight jobs: %v", allowed)
	}
	if suite.usesFile() != ciSuiteFile || suite.keys["name"] != "Full suite" ||
		suite.keys["needs"] != "[changes]" || suite.keys["secrets"] != "inherit" ||
		suite.keys["if"] != "github.event_name != 'pull_request' || contains(github.event.pull_request.labels.*.name, 'ci-full')" ||
		suite.with["lanes"] != "${{ needs.changes.outputs.lanes }}" {
		t.Fatalf("full-suite caller must preserve lane selection, independent lint and secrets: %+v", suite)
	}
	// The suite holds every Preflight leg, so a run that takes the suite skips
	// Preflight: the two conditions partition the events.
	if preflight.keys["if"] != "github.event_name == 'pull_request' && !contains(github.event.pull_request.labels.*.name, 'ci-full') && fromJSON(needs.changes.outputs.lanes)['preflight']" {
		t.Errorf("preflight must run exactly where the suite does not: %q", preflight.keys["if"])
	}
	for scope, level := range permissionRequests(workflowSource(t, ciSuiteFile)) {
		if suite.perms[scope] != level {
			t.Errorf("suite caller grants %s:%q, nested jobs require %q", scope, suite.perms[scope], level)
		}
	}
	if perms, _ := topLevelBlock(workflowSource(t, ciSuiteFile), "permissions"); strings.Contains(perms, ": write") {
		t.Fatal("ordinary suite jobs must retain read-only default permissions")
	}
	seen := map[string]bool{}
	for _, job := range ciJobs(t) {
		if seen[job.id] {
			t.Errorf("job %s is duplicated across orchestration layers", job.id)
		}
		seen[job.id] = true
	}
	on, _ := onBlock(workflowSource(t, ciSuiteFile))
	for _, event := range []string{"pull_request", "push", "workflow_dispatch"} {
		if _, ok := triggerBlock(on, event); ok {
			t.Errorf("suite must be reached through preflight, not a %s trigger", event)
		}
	}
}
